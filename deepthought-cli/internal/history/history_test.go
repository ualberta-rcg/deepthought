package history

import (
	"context"
	"testing"
	"time"

	"annorax/internal/babel"
	"annorax/internal/queen"
)

func TestCollectiveMessagesShape(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "You are Annorax."})
	inc := coll.StartIncursion("list files")

	tx, err := inc.AddTransmission("I'll list the files.", []babel.ToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: babel.FunctionCall{
				Name:      "bash",
				Arguments: `{"command":"ls"}`,
			},
		},
		{
			ID:   "call_2",
			Type: "function",
			Function: babel.FunctionCall{
				Name:      "read",
				Arguments: `{"file_path":"/etc/os-release"}`,
			},
		},
	})
	if err != nil {
		t.Fatalf("AddTransmission error: %v", err)
	}

	// Simulate completed results.
	tx.Probes[0].Result = ResultView{Content: "foo.txt\nbar.txt", Summaries: SummarySet{Full: "foo.txt\nbar.txt"}}
	tx.Probes[1].Result = ResultView{Content: "ID=ubuntu", Summaries: SummarySet{Full: "ID=ubuntu"}}
	inc.MarkCompleted()

	msgs := coll.Messages(SummaryFull)
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(msgs), msgs)
	}

	assertMsg := func(i int, role string) {
		t.Helper()
		if msgs[i].Role != role {
			t.Fatalf("msg[%d].Role = %q, want %q", i, msgs[i].Role, role)
		}
	}

	assertMsg(0, "system")
	assertMsg(1, "user")
	assertMsg(2, "assistant")
	assertMsg(3, "tool")
	assertMsg(4, "tool")

	if len(msgs[2].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls in assistant message, got %d", len(msgs[2].ToolCalls))
	}
	if msgs[3].ToolCallID != "call_1" {
		t.Fatalf("msg[3].ToolCallID = %q, want call_1", msgs[3].ToolCallID)
	}
	if msgs[4].ToolCallID != "call_2" {
		t.Fatalf("msg[4].ToolCallID = %q, want call_2", msgs[4].ToolCallID)
	}
}

func TestMultiTransmissionSerialization(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	inc := coll.StartIncursion("do it")

	tx1, err := inc.AddTransmission("First I will read.", []babel.ToolCall{
		{
			ID:       "call_a",
			Type:     "function",
			Function: babel.FunctionCall{Name: "read", Arguments: `{"file_path":"/tmp/a"}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx1.Probes[0].Result = ResultView{Content: "a content", Summaries: SummarySet{Full: "a content"}}

	tx2, err := inc.AddTransmission("Now I will run bash.", []babel.ToolCall{
		{
			ID:       "call_b",
			Type:     "function",
			Function: babel.FunctionCall{Name: "bash", Arguments: `{"command":"echo done"}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx2.Probes[0].Result = ResultView{Content: "done", Summaries: SummarySet{Full: "done"}}
	inc.MarkCompleted()

	msgs := coll.Messages(SummaryFull)
	// system, user, assistant+tool, assistant+tool = 6
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(msgs))
	}

	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].ID != "call_a" {
		t.Fatalf("msg[2] should be assistant with call_a, got %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "call_a" {
		t.Fatalf("msg[3] should be tool result for call_a, got %+v", msgs[3])
	}
	if msgs[4].Role != "assistant" || len(msgs[4].ToolCalls) != 1 || msgs[4].ToolCalls[0].ID != "call_b" {
		t.Fatalf("msg[4] should be assistant with call_b, got %+v", msgs[4])
	}
	if msgs[5].Role != "tool" || msgs[5].ToolCallID != "call_b" {
		t.Fatalf("msg[5] should be tool result for call_b, got %+v", msgs[5])
	}
}

func TestSummaryFallbacks(t *testing.T) {
	s := SummarySet{
		Full:      "full text",
		Condensed: "condensed text",
		Semantic:  "semantic text",
		Tombstone: "tombstone text",
	}

	if got := s.At(SummaryFull); got != "full text" {
		t.Fatalf("Full fallback: got %q", got)
	}
	if got := s.At(SummaryCondensed); got != "condensed text" {
		t.Fatalf("Condensed: got %q", got)
	}
	if got := s.At(SummarySemantic); got != "semantic text" {
		t.Fatalf("Semantic: got %q", got)
	}
	if got := s.At(SummaryTombstone); got != "tombstone text" {
		t.Fatalf("Tombstone: got %q", got)
	}

	// Empty levels fall back.
	s2 := SummarySet{Condensed: "only condensed"}
	if got := s2.At(SummarySemantic); got != "only condensed" {
		t.Fatalf("expected fallback to condensed, got %q", got)
	}
	if got := s2.At(SummaryTombstone); got != "only condensed" {
		t.Fatalf("expected fallback to condensed, got %q", got)
	}
}

func TestFailedIncursionOmitted(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	inc := coll.StartIncursion("hello")
	inc.Fail("something went wrong")

	msgs := coll.Messages(SummaryFull)
	if len(msgs) != 1 || msgs[0].Role != "system" {
		t.Fatalf("expected only system message, got %+v", msgs)
	}
}

func TestPatternsExtraction(t *testing.T) {
	probe := &Probe{
		Vinculum: Vinculum{ID: newID("probe"), Kind: "probe"},
		Name:     "bash",
		Result: ResultView{
			Content: "foo\nDISCOVERY: build uses Go 1.25\nbar\nLEARNED: cache lives under $SCRATCH\nbaz",
		},
	}

	patterns := probe.ExtractPatterns()
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d: %+v", len(patterns), patterns)
	}
	if patterns[0].Category != "env" {
		t.Fatalf("expected category env, got %q", patterns[0].Category)
	}
	if patterns[0].Content != "build uses Go 1.25" {
		t.Fatalf("unexpected content: %q", patterns[0].Content)
	}
	if patterns[0].SourceProbeID != probe.ID {
		t.Fatalf("expected SourceProbeID to be probe ID")
	}
}

func TestProbeParseError(t *testing.T) {
	inc := &Incursion{}
	tx, err := inc.AddTransmission("bad args", []babel.ToolCall{
		{
			ID:       "call_bad",
			Type:     "function",
			Function: babel.FunctionCall{Name: "bash", Arguments: `not json`},
		},
	})
	if err == nil {
		t.Fatal("expected AddTransmission to report parse error")
	}
	if tx.Probes[0].ArgumentsError == "" {
		t.Fatal("expected ArgumentsError to be set")
	}
	if tx.Probes[0].Arguments == nil {
		t.Fatal("expected Arguments to be empty map, not nil")
	}
}

func TestProbeToBabelPreservesWireID(t *testing.T) {
	probe := &Probe{
		Vinculum:     Vinculum{ID: "probe_x", Kind: "probe"},
		WireID:       "call_x",
		Name:         "read",
		ArgumentsRaw: `{"file_path":"/tmp/x"}`,
		Arguments:    map[string]any{"file_path": "/tmp/x"},
		Decision:     queen.Allow,
		Status:       ProbeCompleted,
	}

	wire := probe.ToBabel()
	if wire.ID != "call_x" || wire.Function.Name != "read" || wire.Function.Arguments != `{"file_path":"/tmp/x"}` {
		t.Fatalf("ToBabel did not preserve wire shape: %+v", wire)
	}
}

func TestActiveIncursion(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	if coll.ActiveIncursion() != nil {
		t.Fatal("expected no active incursion")
	}
	i1 := coll.StartIncursion("one")
	if coll.ActiveIncursion() != i1 {
		t.Fatal("expected i1 to be active")
	}
	i1.MarkCompleted()
	if coll.ActiveIncursion() != nil {
		t.Fatal("expected no active incursion after completion")
	}
}

func TestCondense(t *testing.T) {
	text := "line1\nline2\nline3\nline4"
	got := Condense(text, 2, 100)
	want := "line1\nline2\n..."
	if got != want {
		t.Fatalf("Condense: got %q, want %q", got, want)
	}
}

func TestMemStoreCreateAndGet(t *testing.T) {
	store := NewMemStore()
	coll, err := store.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCollective(coll.ID)
	if err != nil || got != coll {
		t.Fatalf("GetCollective failed: err=%v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
}

func TestVinculumLinks(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s1"})
	if coll.Kind != "collective" {
		t.Fatalf("expected collective kind, got %q", coll.Kind)
	}

	inc := coll.StartIncursion("hello")
	if inc.ParentID != coll.ID {
		t.Fatalf("incursion ParentID = %q, want %q", inc.ParentID, coll.ID)
	}
	if inc.SessionID != "s1" {
		t.Fatalf("incursion SessionID = %q, want s1", inc.SessionID)
	}
	if inc.LeftID != "" {
		t.Fatalf("first incursion should have no LeftID")
	}

	inc2 := coll.StartIncursion("again")
	if inc2.LeftID != inc.ID {
		t.Fatalf("incursion2 LeftID = %q, want %q", inc2.LeftID, inc.ID)
	}
	if inc.RightID != inc2.ID {
		t.Fatalf("incursion RightID = %q, want %q", inc.RightID, inc2.ID)
	}

	tx, _ := inc.AddTransmission("hi", []babel.ToolCall{
		{ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}},
	})
	if tx.ParentID != inc.ID {
		t.Fatalf("transmission ParentID = %q, want %q", tx.ParentID, inc.ID)
	}
	probe := tx.Probes[0]
	if probe.ParentID != tx.ID {
		t.Fatalf("probe ParentID = %q, want %q", probe.ParentID, tx.ID)
	}
	if probe.WireID != "c1" {
		t.Fatalf("probe WireID = %q, want c1", probe.WireID)
	}

	// Generic links.
	found := false
	for _, link := range probe.Links {
		if link.TargetID == tx.ID && link.Relation == "response_to" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected probe to link back to transmission with response_to")
	}
}

func TestReadDocExtension(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	inc := coll.StartIncursion("read")
	tx, _ := inc.AddTransmission("read", []babel.ToolCall{
		{ID: "c1", Type: "function", Function: babel.FunctionCall{Name: "read", Arguments: `{"file_path":"/tmp/x","offset":10,"limit":20}`}},
	})
	probe := tx.Probes[0]
	if probe.ProbeType != "read" {
		t.Fatalf("expected ProbeType read, got %q", probe.ProbeType)
	}
	var ext ReadDocExt
	if err := probe.ExtensionAs(&ext); err != nil {
		t.Fatalf("ExtensionAs failed: %v", err)
	}
	if ext.FilePath != "/tmp/x" || ext.Offset != 10 || ext.Limit != 20 {
		t.Fatalf("unexpected ReadDocExt: %+v", ext)
	}
}

func TestVinculumAge(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SystemPrompt: "sys"})
	start := time.Now()
	age := coll.AgeAt(start.Add(50 * time.Millisecond))
	if age < 0 || age > 100*time.Millisecond {
		t.Fatalf("unexpected age: %v", age)
	}
}

func TestStoreObjectRoundTrip(t *testing.T) {
	store := NewMemStore()
	coll, _ := store.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s1"})
	inc := coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("hi", nil)
	_ = tx
	_ = store.SaveCollective(coll)

	flat := make([]Entity, 0, len(store.byID))
	for _, e := range store.byID {
		flat = append(flat, e)
	}

	loaded, _ := store.GetCollective(coll.ID)
	loaded.RebuildLinks(flat)
	if len(loaded.Incursions) != 1 {
		t.Fatalf("expected 1 incursion after rebuild, got %d", len(loaded.Incursions))
	}
	if len(loaded.Incursions[0].Transmissions) != 1 {
		t.Fatalf("expected 1 transmission after rebuild, got %d", len(loaded.Incursions[0].Transmissions))
	}
}

func TestSummaryEngineExplicit(t *testing.T) {
	// The engine requires a client; with a nil client it still fills rule-based
	// levels and leaves Semantic empty.
	engine := &SummaryEngine{}
	set := SummarySet{}
	ctx := context.Background()
	_ = ctx
	if err := engine.WriteSummarySet(ctx, &set, "line1\nline2\nline3"); err != nil {
		t.Fatalf("WriteSummarySet failed: %v", err)
	}
	if set.Full != "line1\nline2\nline3" {
		t.Fatalf("expected Full set, got %q", set.Full)
	}
	if set.Condensed == "" {
		t.Fatalf("expected Condensed generated")
	}
	if set.Tombstone == "" {
		t.Fatalf("expected Tombstone generated")
	}
}

// TestFileStoreRoundTrip persists a graph to JSONL, reloads it in a fresh
// store, and asserts the graph + flattened wire messages survive intact
// (the property the Continue screen relies on).
func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()

	write := NewFileStore(dir)
	coll, err := write.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	inc := coll.StartIncursion("what is in /tmp?")
	_ = write.SaveObject(inc)
	tx, _ := inc.AddTransmission("looking", []babel.ToolCall{{
		ID: "call_1", Type: "function", Function: babel.FunctionCall{Name: "bash", Arguments: `{"command":"ls /tmp"}`},
	}})
	_ = write.SaveObject(tx)
	for _, p := range tx.Probes {
		p.Status = ProbeCompleted
		p.Result = ResultView{Content: "file1\nfile2", Summary: "2 files"}
		_ = write.SaveObject(p)
	}
	tx2, _ := inc.AddTransmission("found 2 files", nil)
	_ = write.SaveObject(tx2)
	inc.MarkCompleted()
	_ = write.SaveObject(inc)

	// Reload in a brand-new store.
	read := NewFileStore(dir)
	loaded, err := read.GetCollective(coll.ID)
	if err != nil {
		t.Fatalf("GetCollective: %v", err)
	}
	if loaded.SystemPrompt != "sys" {
		t.Errorf("SystemPrompt = %q", loaded.SystemPrompt)
	}
	if len(loaded.Incursions) != 1 {
		t.Fatalf("incursions = %d, want 1", len(loaded.Incursions))
	}
	got := loaded.Incursions[0]
	if got.Prompt != "what is in /tmp?" {
		t.Errorf("Prompt = %q", got.Prompt)
	}
	if len(got.Transmissions) != 2 {
		t.Fatalf("transmissions = %d, want 2", len(got.Transmissions))
	}
	if len(got.Transmissions[0].Probes) != 1 {
		t.Fatalf("probes on tx0 = %d, want 1", len(got.Transmissions[0].Probes))
	}
	if got.Transmissions[0].Probes[0].Result.Summary != "2 files" {
		t.Errorf("probe result summary lost: %q", got.Transmissions[0].Probes[0].Result.Summary)
	}

	// The flattened wire must be reconstructable (the resume path).
	msgs := loaded.Messages(SummaryFull)
	if len(msgs) < 4 { // system + user + assistant(tool) + tool(result) + assistant
		t.Errorf("flattened messages = %d, want >=4", len(msgs))
	}
}

// TestFileStoreListCollectives verifies the Continue-listing metadata.
func TestFileStoreListCollectives(t *testing.T) {
	dir := t.TempDir()

	s := NewFileStore(dir)
	coll, _ := s.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "sys", SessionID: "s"})
	inc := coll.StartIncursion("summarize the readme please")
	_ = s.SaveObject(inc)
	tx, _ := inc.AddTransmission("ok", nil)
	_ = s.SaveObject(tx)

	sums, err := ListCollectives(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("ListCollectives = %d, want 1", len(sums))
	}
	if sums[0].ID != coll.ID {
		t.Errorf("ID = %q, want %q", sums[0].ID, coll.ID)
	}
	if sums[0].Title != "summarize the readme please" {
		t.Errorf("Title = %q", sums[0].Title)
	}
	if sums[0].Incursions != 1 || sums[0].Messages != 1 {
		t.Errorf("counts = incursions %d messages %d, want 1/1", sums[0].Incursions, sums[0].Messages)
	}
}
