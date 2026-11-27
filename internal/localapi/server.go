package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/builtin"
	"github.com/romanvolkov/flows/internal/engine"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
)

func Run(ctx context.Context, home paths.Home, build string) error {
	if err := home.InitRuntime(); err != nil {
		return err
	}
	lock, err := TryLock(filepath.Join(home.Runtime, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := home.Init(); err != nil {
		return err
	}

	state, err := store.Open(ctx, filepath.Join(home.State, "flows.db"))
	if err != nil {
		return err
	}
	defer state.Close()

	sessions := NewSessionManager(state, home.State)
	if _, err := sessions.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile sessions: %w", err)
	}
	runs := NewRunManager(home, state, sessions)
	if err := runs.ReconcileIntents(ctx); err != nil {
		return fmt.Errorf("reconcile launch intents: %w", err)
	}
	defer runs.CloseHosts()

	listener, err := listen(home.Socket())
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go runs.WatchAdoptedHosts(ctx)
	libraryWorker := newLibraryReconciler(home)
	go libraryWorker.run(ctx)
	if err := state.MarkUnfinishedRunsInterrupted(ctx); err != nil {
		return fmt.Errorf("mark interrupted workflows: %w", err)
	}
	notifications, err := builtin.NewNotificationCenter(home.State)
	if err != nil {
		return err
	}
	watches, err := engine.OpenWatchStore(filepath.Join(home.State, "watches"))
	if err != nil {
		return err
	}
	workflows := &workflowRunner{ctx: ctx, state: state, runs: runs, notifications: notifications, watches: watches, library: libraryWorker, cancellations: make(map[string]context.CancelFunc)}
	defer workflows.wg.Wait()
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           handler(Version{Protocol: ProtocolVersion, Build: build}, stop, state, sessions, runs, home, libraryWorker, workflows),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	drained := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			server.Close()
		}
		drained <- err
	}()

	err = server.Serve(listener)
	stop()
	shutdownErr := <-drained
	if errors.Is(err, http.ErrServerClosed) {
		return shutdownErr
	}
	return errors.Join(err, shutdownErr)
}

func listen(path string) (*net.UnixListener, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("service socket path is occupied: %s", path)
		}
		old, dialErr := net.DialTimeout("unix", path, 350*time.Millisecond)
		if dialErr == nil {
			old.Close()
			return nil, fmt.Errorf("a listener already occupies %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("inspect stale socket: %w", dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func handler(version Version, shutdown context.CancelFunc, state *store.Store, sessions *SessionManager, runs *RunManager, home paths.Home, libraryWorker *libraryReconciler, workflows *workflowRunner) http.Handler {
	var harnesses harnessCache
	usageHandler := UsageHandler(state, home)
	questions := QuestionsHandler(workflows.notifications, workflows.questionActive)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB")
			return
		}
		if r.ContentLength != 0 && r.URL.Path != "/v1/runs" && r.URL.Path != "/v1/workflows/run" && r.URL.Path != "/v1/workflows/result" && !strings.HasPrefix(r.URL.Path, "/v1/library/") && !strings.HasPrefix(r.URL.Path, "/v1/settings") && !strings.HasPrefix(r.URL.Path, "/v1/questions/") {
			body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
			n, err := io.Copy(io.Discard, body)
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &tooLarge):
				writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB")
				return
			case err != nil || n != 0:
				writeError(w, http.StatusBadRequest, "invalid_body", "this endpoint expects an empty request body")
				return
			}
		}

		switch r.URL.Path {
		case "/v1/workspace":
			if method(w, r, http.MethodGet) {
				workspace(w, r, state)
			}
		case "/v1/events":
			if method(w, r, http.MethodGet) {
				events(w, r, state)
			}
		case "/v1/version":
			if method(w, r, http.MethodGet) {
				writeJSON(w, http.StatusOK, version)
			}
		case "/v1/health":
			if method(w, r, http.MethodGet) {
				writeJSON(w, http.StatusOK, Health{Status: "ready"})
			}
		case "/v1/shutdown":
			if method(w, r, http.MethodPost) {
				writeJSON(w, http.StatusOK, Health{Status: "stopping"})
				shutdown()
			}
		case "/v1/runs":
			handleRuns(w, r, runs)
		case "/v1/notifications":
			questions(w, r)
		case "/v1/workflows/result":
			workflows.submitHarnessResult(w, r)
		case "/v1/workflows", "/v1/workflows/run":
			workflows.serveHTTP(w, r)
		case "/v1/library":
			handleLibrary(w, r, home, libraryWorker)
		case "/v1/settings", "/v1/settings/validate":
			SettingsHandler(home)(w, r)
			if r.URL.Path == "/v1/settings" && r.Method == http.MethodPatch {
				harnesses.invalidate()
			}
		case "/v1/harnesses":
			if method(w, r, http.MethodGet) {
				result, err := harnesses.list(r.Context(), home)
				if err != nil {
					writeError(w, 500, "settings_error", "could not read settings or probe harnesses")
					return
				}
				writeJSON(w, 200, result)
			}
		case "/v1/usage", "/v1/usage/daily":
			usageHandler(w, r)
		case "/v1/sessions":
			handleSessions(w, r, sessions)
		default:
			if strings.HasPrefix(r.URL.Path, "/v1/questions/") {
				questions(w, r)
				return
			}
			if nodeID, ok := strings.CutPrefix(r.URL.Path, "/v1/nodes/"); ok {
				for _, action := range []string{"stop", "remove", "resume"} {
					if id, matched := strings.CutSuffix(nodeID, "/"+action); matched && id != "" {
						if action == "stop" {
							if r.Method == http.MethodPost && workflows.cancelNode(id) {
								writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
							} else {
								handleNodeStop(w, r, sessions, id)
							}
						} else if action == "remove" {
							handleNodeRemove(w, r, state, sessions, id)
						} else {
							handleNodeResume(w, r, runs, id)
						}
						return
					}
				}
			}
			if attempt, ok := strings.CutPrefix(r.URL.Path, "/v1/preview/"); ok && runs != nil {
				if method(w, r, http.MethodGet) {
					runs.preview(w, attempt)
				}
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/library/") {
				handleLibrary(w, r, home, libraryWorker)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/sessions/") {
				handleSessions(w, r, sessions)
				return
			}
			writeError(w, http.StatusNotFound, "not_found", "unknown service endpoint")
		}
	})
}

func method(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported request method")
	return false
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, Error{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
