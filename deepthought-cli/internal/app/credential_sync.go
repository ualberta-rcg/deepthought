package app

// Credential sync runs after a successful settings sync. For every synced
// provider it compares the local key with the server copy by fingerprint
// (HMAC under a per-user salt, so neither side reveals a value) and plans an
// upload, a download or a conflict. The first time on a machine the whole
// plan is reviewed in an overlay; afterwards uploads and downloads apply on
// their own and only conflicts ask. Downloaded keys go through the normal
// save path, which moves literals into secrets.env and registers them for
// redaction. A server without the endpoints (404/501) just has no sync.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"charm.land/bubbletea/v2"

	"deepthought-cli/internal/config"
	"deepthought-cli/internal/credential"
	"deepthought-cli/internal/tui"
	"deepthought-cli/internal/unimatrix"
)

const credentialSyncKind = "credential-sync"

var errCredentialsUnsupported = errors.New("server does not support credential sync")

type serverCredential struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	BaseURL     string    `json:"base_url"`
	Wire        string    `json:"wire"`
	Kind        string    `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type credentialIndex struct {
	Salt        string             `json:"salt"`
	Vault       bool               `json:"vault"`
	Credentials []serverCredential `json:"credentials"`
}

func unsupported(err error) bool {
	var code serverHTTPError
	return errors.As(err, &code) && (int(code) == http.StatusNotFound || int(code) == http.StatusNotImplemented)
}

func (s *ServerSession) credentialIndex() (credentialIndex, error) {
	var out credentialIndex
	err := s.do("GET", "/api/v1/user/credentials", nil, &out)
	if unsupported(err) {
		return out, errCredentialsUnsupported
	}
	return out, err
}

func (s *ServerSession) fetchCredential(id string) (kind, value string, err error) {
	var out struct{ Kind, Value string }
	err = s.do("GET", "/api/v1/user/credentials/"+id, nil, &out)
	return out.Kind, out.Value, err
}

func (s *ServerSession) storeCredential(p config.Provider, kind, value string) error {
	body, _ := json.Marshal(map[string]string{"name": p.Name, "base_url": p.BaseURL, "wire": p.WireOrDefault(), "kind": kind, "value": value})
	return s.do("PUT", "/api/v1/user/credentials/"+credentialIDFor(p), body, nil)
}

// DeleteCredential removes the server copy of one credential.
func (s *ServerSession) DeleteCredential(id string) error {
	return s.do("DELETE", "/api/v1/user/credentials/"+id, nil, nil)
}

func credentialIDFor(p config.Provider) string {
	return config.CredentialID(p.Name, p.BaseURL, p.WireOrDefault())
}

// localCredential is the key this machine uses for p: an $ENV reference is
// synced as the reference; anything else is a secret value.
func localCredential(p config.Provider) (kind, value string, ok bool) {
	if p.APIKey == "" {
		return "", "", false
	}
	if strings.HasPrefix(p.APIKey, "$") {
		return "env", p.APIKey, true
	}
	v := credential.Resolve(p.APIKey)
	if v == "" {
		return "", "", false
	}
	return "secret", v, true
}

// Credential plan states.
const (
	credUpload   = "upload"
	credDownload = "download"
	credConflict = "conflict"
)

// Choices a review can make for an item.
const (
	credChoiceSkip = iota
	credChoiceUpload
	credChoiceDownload
)

type credItem struct {
	Provider    config.Provider
	ID          string
	State       string
	LocalKind   string
	LocalValue  string
	ServerKind  string
	Fingerprint string // server fingerprint tail source
	Choice      int
}

// planCredentials diffs the local providers against the server index.
func planCredentials(f config.File, idx credentialIndex) []credItem {
	salt, _ := hex.DecodeString(idx.Salt)
	byID := map[string]serverCredential{}
	for _, c := range idx.Credentials {
		byID[c.ID] = c
	}
	var out []credItem
	for _, p := range f.Providers {
		if !p.SyncsCredential() || p.BaseURL == "" {
			continue
		}
		id := credentialIDFor(p)
		remote, onServer := byID[id]
		kind, value, local := localCredential(p)
		it := credItem{Provider: p, ID: id, LocalKind: kind, LocalValue: value, ServerKind: remote.Kind, Fingerprint: remote.Fingerprint}
		switch {
		case local && !onServer:
			if kind == "secret" && !idx.Vault {
				continue
			}
			it.State, it.Choice = credUpload, credChoiceUpload
		case !local && onServer:
			it.State, it.Choice = credDownload, credChoiceDownload
		case local && onServer:
			if len(salt) > 0 && config.CredentialFingerprint(salt, value) == remote.Fingerprint {
				continue
			}
			it.State, it.Choice = credConflict, credChoiceSkip
		default:
			continue
		}
		out = append(out, it)
	}
	return out
}

type credentialReviewRecord struct {
	Reviewed bool
}

func credentialRecordKey(s *ServerSession) string { return s.URL + "|" + s.User }

type credentialPlanMsg struct {
	Review  []credItem
	Vault   bool
	Summary string
	Err     error
}

type credentialAppliedMsg struct {
	Summary string
	Err     error
}

// credentialCheckCmd fetches the index and either applies the plan (after
// the first review on this machine) or returns it for review.
func credentialCheckCmd(live *Settings, sess *ServerSession) tea.Cmd {
	return func() tea.Msg {
		idx, err := sess.credentialIndex()
		if errors.Is(err, errCredentialsUnsupported) {
			return credentialPlanMsg{}
		}
		if err != nil {
			return credentialPlanMsg{Err: err}
		}
		f, err := live.Saved()
		if err != nil {
			return credentialPlanMsg{Err: err}
		}
		items := planCredentials(f, idx)
		if len(items) == 0 {
			return credentialPlanMsg{}
		}
		var rec credentialReviewRecord
		if s := live.LocalStore(); s != nil {
			_ = s.ReadRecord(credentialSyncKind, credentialRecordKey(sess), &rec)
		}
		if !rec.Reviewed {
			return credentialPlanMsg{Review: items, Vault: idx.Vault}
		}
		var auto, conflicts []credItem
		for _, it := range items {
			if it.State == credConflict {
				conflicts = append(conflicts, it)
			} else {
				auto = append(auto, it)
			}
		}
		summary, err := applyCredentials(live, sess, auto)
		return credentialPlanMsg{Review: conflicts, Vault: idx.Vault, Summary: summary, Err: err}
	}
}

// credentialApplyCmd applies reviewed choices and remembers the review.
func credentialApplyCmd(live *Settings, sess *ServerSession, items []credItem) tea.Cmd {
	return func() tea.Msg {
		summary, err := applyCredentials(live, sess, items)
		if s := live.LocalStore(); s != nil && err == nil {
			_ = s.WriteRecord(credentialSyncKind, credentialRecordKey(sess), credentialReviewRecord{Reviewed: true})
		}
		return credentialAppliedMsg{Summary: summary, Err: err}
	}
}

func applyCredentials(live *Settings, sess *ServerSession, items []credItem) (string, error) {
	up, down := 0, 0
	for _, it := range items {
		switch it.Choice {
		case credChoiceUpload:
			if err := sess.storeCredential(it.Provider, it.LocalKind, it.LocalValue); err != nil {
				return credentialSummary(up, down), fmt.Errorf("upload %s: %w", it.Provider.Name, err)
			}
			up++
		case credChoiceDownload:
			_, value, err := sess.fetchCredential(it.ID)
			if err == nil && value == "" {
				err = errors.New("empty value")
			}
			if err == nil {
				err = live.BindCredential(it.Provider, value)
			}
			if err != nil {
				return credentialSummary(up, down), fmt.Errorf("download %s: %w", it.Provider.Name, err)
			}
			down++
		}
	}
	return credentialSummary(up, down), nil
}

func credentialSummary(up, down int) string {
	var parts []string
	if up > 0 {
		parts = append(parts, fmt.Sprintf("%d uploaded", up))
	}
	if down > 0 {
		parts = append(parts, fmt.Sprintf("%d downloaded", down))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Provider keys: " + strings.Join(parts, ", ")
}

// BindCredential sets the key for the provider with p's identity. Literal
// values are moved into secrets.env by the save path.
func (s *Settings) BindCredential(p config.Provider, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == nil {
		return fmt.Errorf("database-backed settings required")
	}
	saved, err := s.local.Saved()
	if err != nil {
		return err
	}
	f := cloneFile(saved.File)
	id := credentialIDFor(p)
	found := false
	for i := range f.Providers {
		if credentialIDFor(f.Providers[i]) == id {
			f.Providers[i].APIKey = key
			found = true
		}
	}
	if !found {
		return fmt.Errorf("provider %s is no longer configured", p.Name)
	}
	cfg, err := s.local.SaveSynced(saved.File, f)
	if err != nil {
		return err
	}
	s.cfg, s.revision, s.pool = cfg, cfg.Revision, unimatrix.NewPool()
	return nil
}

// credentialReviewRows turns a plan into overlay rows. Values are never
// shown; env references are, since they are names, not keys.
func credentialReviewRows(items []credItem) []tui.CredentialReviewRow {
	rows := make([]tui.CredentialReviewRow, len(items))
	for i, it := range items {
		r := tui.CredentialReviewRow{Provider: it.Provider.Name}
		local := "key on this machine"
		if it.LocalKind == "env" {
			local = it.LocalValue + " on this machine"
		}
		switch it.State {
		case credUpload:
			r.Detail = local + ", not on the server"
			r.Options = []string{"Upload", "Skip"}
		case credDownload:
			r.Detail = "on the server, no key here" + fingerprintTail(it.Fingerprint)
			r.Options = []string{"Download", "Skip"}
		case credConflict:
			r.Detail = local + " differs from the server copy" + fingerprintTail(it.Fingerprint)
			r.Options = []string{"Skip", "Keep local (upload)", "Take server"}
		}
		rows[i] = r
	}
	return rows
}

func fingerprintTail(fp string) string {
	if len(fp) < 4 {
		return ""
	}
	return " (…" + fp[len(fp)-4:] + ")"
}

// applyReviewChoices maps overlay option indexes back onto plan choices.
func applyReviewChoices(items []credItem, picks []int) []credItem {
	out := append([]credItem(nil), items...)
	for i := range out {
		pick := 0
		if i < len(picks) {
			pick = picks[i]
		}
		switch out[i].State {
		case credUpload:
			out[i].Choice = map[int]int{0: credChoiceUpload, 1: credChoiceSkip}[pick]
		case credDownload:
			out[i].Choice = map[int]int{0: credChoiceDownload, 1: credChoiceSkip}[pick]
		case credConflict:
			out[i].Choice = map[int]int{0: credChoiceSkip, 1: credChoiceUpload, 2: credChoiceDownload}[pick]
		}
	}
	return out
}

func (m RootModel) credentialPlan(msg credentialPlanMsg) (tea.Model, tea.Cmd) {
	next, cmd := m.credentialApplied(msg.Summary, msg.Err)
	m = next.(RootModel)
	if len(msg.Review) > 0 && msg.Err == nil {
		m.credPlan = msg.Review
		m.pushOverlay(tui.NewCredentialReview(credentialReviewRows(msg.Review), msg.Vault))
	}
	return m, cmd
}

func (m RootModel) credentialApplied(summary string, err error) (tea.Model, tea.Cmd) {
	if errors.Is(err, ErrSessionExpired) && m.server != nil {
		m.sessionExpired()
		return m, nil
	}
	if err != nil {
		m.chat = m.chat.Notice("⚠ Provider key sync: " + credential.Redact(err.Error()))
	}
	if summary != "" {
		m.settings = m.settings.Refresh()
		m.modelsScr = m.modelsScr.Refresh()
		m.chat = m.chat.Notice(summary)
	}
	return m, nil
}
