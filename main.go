package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// matchGlob checks whether path matches any of the provided glob patterns.
// If patterns is empty, it returns true (match all).
func matchGlob(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		// Also try matching on the base name for convenience (e.g., "*.cs").
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// isExcluded returns true if the relative path matches an exclusion rule.
func isExcluded(rel string, excludes []string) bool {
	for _, ex := range excludes {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		// Treat trailing slash as directory prefix exclusion.
		if strings.HasSuffix(ex, string(os.PathSeparator)) || strings.HasSuffix(ex, "/") {
			prefix := strings.TrimSuffix(strings.TrimSuffix(ex, "/"), string(os.PathSeparator))
			if prefix == "." || prefix == "" {
				return true
			}
			if rel == prefix || strings.HasPrefix(rel, prefix+string(os.PathSeparator)) {
				return true
			}
		}
		// Glob-style exclusion (match against rel and basename)
		if ok, _ := filepath.Match(ex, rel); ok {
			return true
		}
		if ok, _ := filepath.Match(ex, filepath.Base(rel)); ok {
			return true
		}
	}
	return false
}

type runner struct {
	cmdStr  string
	cwd     string
	mu      sync.Mutex
	current *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
}

func newRunner(cwd, cmdStr string) *runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &runner{cmdStr: cmdStr, cwd: cwd, ctx: ctx, cancel: cancel}
}

// run starts the command, cancelling any previous one.
func (r *runner) run() {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Cancel previous if running
	if r.current != nil && r.current.Process != nil {
		_ = r.current.Process.Kill()
	}

	cmd := shellCommand(r.cmdStr)
	cmd.Dir = r.cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	// Inherit environment
	cmd.Env = os.Environ()

	// Start in a goroutine so we don't block caller
	if err := cmd.Start(); err != nil {
		log.Printf("failed to start command: %v", err)
		return
	}
	r.current = cmd
	go func(c *exec.Cmd) {
		_ = c.Wait()
	}(cmd)
}

// stop cancels the current run gracefully (SIGTERM on Unix, Kill on Windows).
func (r *runner) stopGracefully(timeout time.Duration) {
	r.mu.Lock()
	cmd := r.current
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(os.Interrupt)
		// Also attempt SIGTERM
		if p, ok := cmd.Process.(*os.Process); ok {
			// Ignore error; not all platforms expose SIGTERM this way
			_ = p.Signal(os.Signal(syscallSIGTERM()))
		}
	} else {
		// Best-effort on Windows
		_ = cmd.Process.Kill()
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
	}
}

// shellCommand returns a platform-appropriate shell command.
func shellCommand(s string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", s)
	}
	return exec.Command("sh", "-lc", s)
}

// syscallSIGTERM provides SIGTERM value without importing syscall directly to keep lint simple.
func syscallSIGTERM() os.Signal {
	type sigterm interface{ Signal() }
	// Fallback for non-Unix will never be used due to GOOS check.
	return os.Signal(syscallSIGTERMValue)
}

// Small trick to avoid direct syscall import in this snippet; we fill at init by platform.
var syscallSIGTERMValue os.Signal

func init() {
	// Use reflection via the syscall package only when available.
	// Importing syscall is fine, keep it simple.
	syscallSIGTERMValue = getSIGTERM()
}

func getSIGTERM() os.Signal {
	// Import here to avoid linter warnings at top for unused on Windows builds.
	type sig = os.Signal
	// On Unix, we can just reference syscall.SIGTERM.
	// We use a tiny helper function compiled for all platforms but the value is ignored on Windows.
	return signalSigterm()
}

// signalSigterm is split for clarity; implemented generically.
func signalSigterm() os.Signal {
	// Bring in syscall here.
	type sig = os.Signal
	// Use a build-tag-less approach: reflect not needed; we import syscall.
	return getSyscallSigterm()
}

func getSyscallSigterm() os.Signal {
	// On non-Unix this is 15, but will be ignored.
	return os.Signal(15)
}

func main() {
	var (
		rootDir    string
		extStr     string
		exceptStr  string
		runCmd     string
		debounceMS int
		verbose    bool
		noInitial  bool
	)

	flag.StringVar(&rootDir, "dir", ".", "directory to watch (recursively)")
	flag.StringVar(&extStr, "extensions", "", "comma-separated glob patterns to include (e.g. '*.cs,*.go')")
	flag.StringVar(&exceptStr, "except", "", "comma-separated paths or globs to exclude (e.g. 'some.cs,dir/')")
	flag.StringVar(&runCmd, "run", "", "command to run on changes")
	flag.IntVar(&debounceMS, "debounce", 250, "debounce window in milliseconds")
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.BoolVar(&noInitial, "no-initial-run", false, "do not run the command once at startup")
	flag.Parse()

	if runCmd == "" {
		fmt.Fprintln(os.Stderr, "--run is required")
		os.Exit(2)
	}

	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		log.Fatal(err)
	}

	exts := splitCSV(extStr)
	excepts := normalizeExcludes(splitCSV(exceptStr))

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	// Setup context cancellation on SIGINT/SIGTERM.
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, os.Interrupt)
	// Best-effort add SIGTERM number; on Windows this is ignored.
	// (We can't import syscall.SIGTERM portably here in one file without tags.)

	run := newRunner(rootAbs, runCmd)

	// Recursively add directories
	if err := addTree(w, rootAbs, excepts, verbose); err != nil {
		log.Fatal(err)
	}

	// Debounce timer
	debounce := time.Duration(debounceMS) * time.Millisecond
	var mu sync.Mutex
	var timer *time.Timer
	trigger := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer == nil {
			timer = time.NewTimer(debounce)
			go func() {
				<-timer.C
				run.run()
				mu.Lock()
				timer = nil
				mu.Unlock()
			}()
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(debounce)
	}

	if !noInitial {
		if verbose {
			log.Println("initial run")
		}
		run.run()
	}

	errorsCh := make(chan error, 1)
	go func() {
		for {
			select {
			case e, ok := <-w.Events:
				if !ok {
					return
				}
				// Normalize relative path
				rel, _ := filepath.Rel(rootAbs, e.Name)
				rel = filepath.ToSlash(rel)

				if isExcluded(rel, excepts) {
					if verbose {
						log.Printf("exclude: %s", rel)
					}
					continue
				}

				// If a new dir is created, watch it recursively
				if (e.Op & fsnotify.Create) == fsnotify.Create {
					if info, err := os.Stat(e.Name); err == nil && info.IsDir() {
						_ = addTree(w, e.Name, excepts, verbose)
						continue
					}
				}

				// Only trigger on file changes we care about
				if matchGlob(rel, exts) || matchGlob(filepath.Base(rel), exts) {
					if verbose {
						log.Printf("change: %s (%s)", rel, opString(e.Op))
					}
					trigger()
				}

			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				errorsCh <- err
			}
		}
	}()

	// Wait for signal or error
	select {
	case s := <-sigc:
		log.Printf("signal: %v — shutting down", s)
	case err := <-errorsCh:
		log.Printf("watcher error: %v", err)
	}

	// Graceful stop of current run
	run.stopGracefully(3 * time.Second)
}

func opString(op fsnotify.Op) string {
	var parts []string
	if op&fsnotify.Create != 0 {
		parts = append(parts, "CREATE")
	}
	if op&fsnotify.Write != 0 {
		parts = append(parts, "WRITE")
	}
	if op&fsnotify.Remove != 0 {
		parts = append(parts, "REMOVE")
	}
	if op&fsnotify.Rename != 0 {
		parts = append(parts, "RENAME")
	}
	if op&fsnotify.Chmod != 0 {
		parts = append(parts, "CHMOD")
	}
	return strings.Join(parts, "|")
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	scan := bufio.NewScanner(strings.NewReader(s))
	scan.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		for i, b := range data {
			if b == ',' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for scan.Scan() {
		part := strings.TrimSpace(scan.Text())
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeExcludes(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = filepath.ToSlash(strings.TrimSpace(x))
		out = append(out, x)
	}
	return out
}

func addTree(w *fsnotify.Watcher, root string, excludes []string, verbose bool) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(rootAbs, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if rel != "" && isExcluded(rel, excludes) {
			if d.IsDir() {
				if verbose {
					log.Printf("skip dir: %s", rel)
				}
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if err := w.Add(path); err != nil {
				// Ignore watch limit errors to continue as far as possible
				if verbose {
					log.Printf("watch add failed: %s: %v", path, err)
				}
				// If it's a non-recoverable error, bubble up
				if !isRecoverable(err) {
					return err
				}
			}
			if verbose {
				log.Printf("watching: %s", path)
			}
		}
		return nil
	})
}

func isRecoverable(err error) bool {
	// Best-effort: treat all as recoverable except nil
	return !errors.Is(err, os.ErrPermission)
}
