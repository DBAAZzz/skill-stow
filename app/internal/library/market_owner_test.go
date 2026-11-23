package library

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestClawHubSearchResultsCarryOwnerAvatar(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(loadMarketFixture(t, "clawhub_search.json"))
	}))
	defer ts.Close()
	mc := newTestMarketClient(t, ts, t.TempDir(), nil)

	items, err := mc.SearchMarketSource(context.Background(), MarketClawHub, "pdf")
	if err != nil {
		t.Fatalf("SearchMarketSource: %v", err)
	}
	withImage := 0
	for _, it := range items {
		if it.OwnerImage != "" {
			withImage++
		}
	}
	if withImage == 0 {
		t.Fatalf("ClawHub search results have no owner avatar; want native.owner.image copied")
	}
}

func TestClawHubOwnerImageLooksUpTheSkillDetail(t *testing.T) {
	var gotPath, gotOwner string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotOwner = r.URL.Path, r.URL.Query().Get("ownerHandle")
		switch gotOwner {
		case "spclaudehome":
			w.Write([]byte(`{"skill":{"slug":"skill-vetter"},"owner":{"handle":"spclaudehome","image":"https://avatars.githubusercontent.com/u/257035288?v=4"}}`))
		case "plain":
			w.Write([]byte(`{"owner":{"handle":"plain","image":"http://example.com/a.png"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	mc := newTestMarketClient(t, ts, t.TempDir(), nil)

	img, err := mc.ClawHubOwnerImage(context.Background(), "spclaudehome", "skill-vetter")
	if err != nil {
		t.Fatalf("ClawHubOwnerImage: %v", err)
	}
	if gotPath != "/api/v1/skills/skill-vetter" || gotOwner != "spclaudehome" {
		t.Errorf("requested %s?ownerHandle=%s", gotPath, gotOwner)
	}
	if img != "https://avatars.githubusercontent.com/u/257035288?v=4" {
		t.Errorf("image = %q", img)
	}
	if img, err := mc.ClawHubOwnerImage(context.Background(), "plain", "x"); err != nil || img != "" {
		t.Errorf("non-https avatar = %q, %v; want empty, nil", img, err)
	}
	if _, err := mc.ClawHubOwnerImage(context.Background(), "missing", "x"); err == nil {
		t.Error("404 should be an error")
	}
	if _, err := mc.ClawHubOwnerImage(context.Background(), "../etc", "x"); err == nil {
		t.Error("bad owner should be rejected before any request")
	}
}

func TestFetchClawHubAvatarCachesPerOwner(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(mustPNG(t, 96, 96, color.NRGBA{R: 255, A: 255}))
	}))
	defer ts.Close()
	f := &IconFetcher{Client: ts.Client(), Dir: t.TempDir()}

	path, err := f.FetchClawHubAvatar(context.Background(), "awspace", ts.URL+"/u/1?v=4")
	if err != nil {
		t.Fatalf("FetchClawHubAvatar: %v", err)
	}
	if want := filepath.Join(f.Dir, "clawhub", "awspace.png"); path != want {
		t.Errorf("cache path = %s, want %s", path, want)
	}
	if b, ok := f.LoadClawHubAvatar("awspace"); !ok || len(b) == 0 {
		t.Error("cached avatar not readable")
	}
	if _, ok := f.LoadAvatar("awspace"); ok {
		t.Error("ClawHub avatar must not be stored as a GitHub avatar")
	}
	if _, err := f.FetchClawHubAvatar(context.Background(), "awspace", "http://example.com/a.png"); err == nil {
		t.Error("non-https avatar URL should be rejected")
	}
	if _, err := f.FetchClawHubAvatar(context.Background(), "a/b", ts.URL); err == nil {
		t.Error("bad owner should be rejected")
	}
}
