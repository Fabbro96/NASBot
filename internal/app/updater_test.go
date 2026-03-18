package app

import "testing"

func TestIsNewerRelease(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "v1.2.0", current: "v1.1.9", want: true},
		{latest: "1.2.0", current: "1.2.0", want: false},
		{latest: "v1.2.0", current: "dev", want: true},
		{latest: "not-a-tag", current: "v1.0.0", want: false},
		{latest: "v1.0.0", current: "v2.0.0", want: false},
	}

	for _, tt := range tests {
		if got := isNewerRelease(tt.latest, tt.current); got != tt.want {
			t.Fatalf("isNewerRelease(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestPickAssetFallsBackToAnyAsset(t *testing.T) {
	rel := githubRelease{TagName: "v1.2.3", Assets: []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{Name: "something-else", BrowserDownloadURL: "https://example.com/x"},
	}}

	name, url, ok := pickAsset(rel)
	if !ok {
		t.Fatalf("expected fallback asset pick")
	}
	if name != "something-else" || url == "" {
		t.Fatalf("unexpected picked asset: name=%q url=%q", name, url)
	}
}
