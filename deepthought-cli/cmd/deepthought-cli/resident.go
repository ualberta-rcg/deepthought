package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"deepthought-cli/internal/alcove"
	"deepthought-cli/internal/app"
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/transwarp"
)

func handleResidentVerb(args []string) (attachID string, handled bool) {
	if len(args) == 0 {
		return "", false
	}
	switch args[0] {
	case "daemon":
		path := ""
		if len(args) > 1 {
			path = args[1]
		}
		runResidentDaemon(path)
		return "", true
	case "ls", "status":
		response, err := sendControl(transwarp.Request{Operation: transwarp.ReportStatus})
		if err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
			return "", true
		}
		if len(response.Sessions) == 0 {
			fmt.Println("no resident sessions")
		}
		for _, session := range response.Sessions {
			fmt.Printf("%s\t%s\tattached=%d\tapproval=%v\n", session.ID, session.State, session.Attached, session.WaitingApproval)
		}
		return "", true
	case "stop":
		request := transwarp.Request{Operation: transwarp.Stop}
		if len(args) > 1 {
			request.SessionID = args[1]
		}
		if _, err := sendControl(request); err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
		}
		return "", true
	case "attach":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: deepthought-cli attach <session-id>")
			return "", true
		}
		if err := runAttached(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
			return "", true
		}
		return "", true
	}
	return "", false
}

func sendControl(request transwarp.Request) (transwarp.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return transwarp.Send(ctx, transwarp.SocketPath(), request)
}

func startResident(configPath string) error {
	if _, err := sendControl(transwarp.Request{Operation: transwarp.ReportStatus}); err == nil {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	name, args := executable, []string{"daemon", configPath}
	envelope := alcove.DefaultEnvelope()
	if envelope.Available() {
		name, args = envelope.Wrap(executable, "daemon", configPath)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if _, err := sendControl(transwarp.Request{Operation: transwarp.ReportStatus}); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("resident daemon did not create %s", transwarp.SocketPath())
}

func runResidentDaemon(configPath string) {
	cfg, path, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	deps, store := buildRuntime(cfg, path, "")
	if store == nil {
		return
	}
	defer store.Close()
	manager := transwarp.NewManager()
	manager.Audit = auditDirective
	ctx, cancel := signalContext()
	defer cancel()
	hub := &residentHub{ctx: ctx, deps: deps, store: store, runners: map[string]*app.SessionRunner{}}
	manager.OnControl = hub.control
	manager.OnStream = hub.stream
	go func() {
		ticker := time.NewTicker(3 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe, cancel := context.WithTimeout(ctx, 30*time.Second)
				_, _ = deps.Scheduler.Reconcile(probe)
				cancel()
			}
		}
	}()
	manager.OnRefresh = func(operation string) error {
		if operation == "refresh_config" {
			return deps.Live.Reload()
		}
		return deps.ReloadSkills()
	}
	if err := manager.Serve(ctx, transwarp.SocketPath()); err != nil {
		fmt.Fprintln(os.Stderr, "deepthought-cli:", err)
	}
	cancel()
	hub.mu.Lock()
	runners := make([]*app.SessionRunner, 0, len(hub.runners))
	for _, r := range hub.runners {
		r.Stop()
		runners = append(runners, r)
	}
	hub.mu.Unlock()
	for _, r := range runners {
		select {
		case <-r.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func auditDirective(request transwarp.Request) {
	dataDir, err := config.DataDir()
	if err != nil {
		return
	}
	drone, err := history.NewDrone("control_directive", "daemon", request)
	if err != nil {
		return
	}
	raw, _ := json.Marshal(drone)
	file, err := os.OpenFile(filepath.Join(dataDir, "control-audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = file.Write(append(raw, '\n'))
		_ = file.Close()
	}
}
