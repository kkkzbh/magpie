package gateway

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// The magpie serving the gateway fetches a signed-in account's list itself,
// without the app's window to ask: `magpie serve` offered WorkBuddy AI
// magpie's built-in list until `magpie provider refresh` (#1805).
func TestServingFetchesASignedInAccountsList(t *testing.T) {
	var asked atomic.Int32
	wbSignedIn(t, provider.WorkBuddyAIID, "workbuddy-desktop-ai", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config" {
			w.WriteHeader(404)
			return
		}
		asked.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":0,"msg":"OK","data":{"agents":[{"name":"cli","models":["hy4-preview","deepseek-v4.1-flash"]}],
			"models":[{"id":"hy4-preview","name":"Hy4 preview"},{"id":"deepseek-v4.1-flash","name":"Deepseek-V4.1-Flash"}]}}`)
	}))
	t.Setenv("MAGPIE_ADDR", "127.0.0.1:0")
	// asked once, a moment after the gateway starts
	t.Cleanup(provider.NewListedForTest(time.Millisecond, time.Hour))
	listed := func() bool {
		p, err := provider.Find(provider.WorkBuddyAIID)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Available() {
			if m.ID == "deepseek-v4.1-flash" {
				return true
			}
		}
		return false
	}
	if listed() {
		t.Fatal("the vendor's model listed before its list was fetched")
	}

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- New().ListenAndServe(ctx) }()
	defer func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
		// the fetch that listed it has returned, and no other starts
		for deadline := time.Now().Add(10 * time.Second); provider.FetchingNew() && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); !listed(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the gateway never fetched the list: asked %d times", asked.Load())
		}
	}
}
