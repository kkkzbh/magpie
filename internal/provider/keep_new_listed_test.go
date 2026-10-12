package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A magpie serving the gateway with no window open (`magpie serve`) asks
// for the list of an account that has none by itself: one signed in while
// it runs, and again after a first try that failed, with nothing calling
// FetchNew. A listed account isn't asked again (#1805: WorkBuddy AI offered
// magpie's built-in 16 models until `magpie provider refresh`).
func TestKeepNewListed(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	wbTokens.Lock()
	wbTokens.m = map[string]wbCreds{}
	wbTokens.Unlock()
	t.Cleanup(NewListedForTest(time.Millisecond, 5*time.Millisecond))
	oldRetry := newFetchRetry
	newFetchRetry = 20 * time.Millisecond
	t.Cleanup(func() {
		newFetchRetry = oldRetry
		wbTokens.Lock()
		wbTokens.m = map[string]wbCreds{}
		wbTokens.Unlock()
		forgetAccountCaches()
	})

	var asked atomic.Int32
	var down atomic.Bool
	down.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config" {
			w.WriteHeader(404)
			return
		}
		asked.Add(1)
		if down.Load() {
			w.WriteHeader(502)
			return
		}
		w.Write([]byte(`{"code":0,"data":{"agents":[{"name":"cli","models":["hy4-preview","deepseek-v4.1-flash"]}],
			"models":[{"id":"hy4-preview","name":"Hy4 preview"},{"id":"deepseek-v4.1-flash","name":"Deepseek-V4.1-Flash"}]}}`))
	}))
	defer srv.Close()
	old := wbEndpoint
	wbEndpoint = srv.URL
	defer func() { wbEndpoint = old }()
	listed := func() bool {
		p, err := Find("workbuddy")
		if err != nil {
			return false
		}
		for _, m := range p.Available() {
			if m.ID == "hy4-preview" {
				return true
			}
		}
		return false
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: asked %d times, listed %v", what, asked.Load(), listed())
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); KeepNewListed(ctx) }()
	defer func() { cancel(); <-done }()

	// WorkBuddy signs itself in while the gateway runs, the vendor not
	// answering yet: asked, and nothing listed
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	writeFile(t, wbAuthFile(home), map[string]any{
		"account": map[string]any{"uid": "u1", "nickname": "旅行者"},
		"auth": map[string]any{"accessToken": "a", "refreshToken": "r",
			"expiresAt": future, "refreshExpiresAt": future, "domain": "www.codebuddy.cn"},
	})
	forgetAccountCaches()
	waitFor("the new account's list never asked for", func() bool { return asked.Load() > 0 })
	if listed() {
		t.Fatal("a list the vendor didn't give is listed")
	}

	// the vendor answers again: the list comes, no one having asked for it
	down.Store(false)
	waitFor("the list not asked for again after a failed try", listed)

	// and once listed it isn't asked for on each round
	n := asked.Load()
	time.Sleep(100 * time.Millisecond)
	if got := asked.Load(); got != n {
		t.Fatalf("a listed account asked again: %d times, then %d", n, got)
	}
}
