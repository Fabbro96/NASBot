package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file cover the release verification path, which is the only
// thing standing between a compromised or broken release and a bot that installs
// and runs it as root on the NAS. Every case is hermetic: the checksum manifest is
// served by an in-process loopback HTTP server (the code under test builds its own
// http.Client, so a transport cannot be injected), and the asset under
// verification is a file in t.TempDir().

const fixtureAsset = "nasbot"

var fixtureDigest = sha256.Sum256([]byte("#!/bin/sh\necho fake release\n"))

// writeFixtureAsset puts the asset in dir and returns its path.
func writeFixtureAsset(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, fixtureAsset)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake release\n"), 0o755); err != nil {
		t.Fatalf("cannot write the fixture asset: %v", err)
	}
	return path
}

// manifestServer serves body at /SHA256SUMS.txt and counts the requests, so a
// test can assert the manifest was fetched exactly once.
func manifestServer(t *testing.T, body string) (*httptest.Server, *int) {
	t.Helper()

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+checksumsAssetName {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestParseChecksumLine pins the manifest parser, which is the component that
// decides whether a digest is accepted at all.
//
// The defects it covers: a parser that took the first field as the digest
// without checking it is 64 hex characters would accept a truncated or
// non-hexadecimal line; one that did not strip the sha256sum binary-mode '*'
// or a leading "./" would silently skip a valid entry and refuse an honest
// release; one that compared names case-sensitively or un-trimmed would refuse
// a manifest that is perfectly valid.
func TestParseChecksumLine(t *testing.T) {
	digest := hex.EncodeToString(fixtureDigest[:])
	other := strings.Repeat("ab", 32)

	cases := []struct {
		name     string
		manifest string
		asset    string
		want     string
		wantOK   bool
	}{
		{
			name:     "two space separated",
			manifest: digest + "  " + fixtureAsset + "\n",
			asset:    fixtureAsset,
			want:     digest, wantOK: true,
		},
		{
			name:     "binary mode star",
			manifest: digest + " *" + fixtureAsset + "\n",
			asset:    fixtureAsset,
			want:     digest, wantOK: true,
		},
		{
			name:     "leading dot slash",
			manifest: digest + "  ./" + fixtureAsset + "\n",
			asset:    fixtureAsset,
			want:     digest, wantOK: true,
		},
		{
			name:     "uppercase digest is normalised",
			manifest: strings.ToUpper(digest) + "  " + fixtureAsset + "\n",
			asset:    fixtureAsset,
			want:     digest, wantOK: true,
		},
		{
			name:     "crlf line endings and padding",
			manifest: "\r\n  " + digest + "   " + fixtureAsset + "  \r\n",
			asset:    fixtureAsset,
			want:     digest, wantOK: true,
		},
		{
			name: "comments and blank lines are skipped",
			manifest: "# sha256sum -c manifest\n\n" +
				digest + "  " + fixtureAsset + "\n\n",
			asset: fixtureAsset,
			want:  digest, wantOK: true,
		},
		{
			name: "the right line is picked out of several",
			manifest: other + "  nasbot-arm64\n" +
				digest + "  " + fixtureAsset + "\n",
			asset: fixtureAsset,
			want:  digest, wantOK: true,
		},
		{
			name:     "asset absent from the manifest",
			manifest: digest + "  nasbot-arm64\n",
			asset:    fixtureAsset,
			wantOK:   false,
		},
		{
			name:     "empty manifest",
			manifest: "",
			asset:    fixtureAsset,
			wantOK:   false,
		},
		{
			name:     "digest of the wrong length",
			manifest: "deadbeef  " + fixtureAsset + "\n",
			asset:    fixtureAsset,
			wantOK:   false,
		},
		{
			name: "digest of the right length but not hexadecimal",
			// 64 characters, none of them hex: a parser that only counted
			// would hand this to hex.DecodeString or, worse, to a comparison
			// that never matches.
			manifest: strings.Repeat("z", 64) + "  " + fixtureAsset + "\n",
			asset:    fixtureAsset,
			wantOK:   false,
		},
		{
			name:     "malformed line with extra fields",
			manifest: digest + "  " + fixtureAsset + " extra\n",
			asset:    fixtureAsset,
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseChecksumLine(tc.manifest, tc.asset)
			if ok != tc.wantOK {
				t.Fatalf("parseChecksumLine ok = %v, want %v (digest %q)", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Errorf("digest = %q, want %q", got, tc.want)
			}
			if !ok && got != "" {
				t.Errorf("a refused lookup must return an empty digest, got %q", got)
			}
		})
	}
}

// TestVerifyAssetChecksumAcceptsMatchingDigest is the success path.
//
// Defect covered: a verification that returned nil without comparing anything
// (a stub, a comparison against an empty string that cannot match a real digest
// but passes a nil-check on a missing manifest) would let any binary through.
func TestVerifyAssetChecksumAcceptsMatchingDigest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	digest := hex.EncodeToString(fixtureDigest[:])
	srv, hits := manifestServer(t, digest+"  "+fixtureAsset+"\n")

	rel := releaseCandidate{
		Tag:          "v9.9.9",
		AssetName:    fixtureAsset,
		AssetURL:     srv.URL + "/" + fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName,
	}

	if err := verifyAssetChecksum(dir, asset, rel); err != nil {
		t.Fatalf("a matching digest must be accepted, got %v", err)
	}
	if *hits != 1 {
		t.Errorf("expected the manifest to be fetched once, got %d fetches", *hits)
	}
}

// TestVerifyAssetChecksumAcceptsUppercaseManifest: sha256sum writes lower case,
// but a digest is a digest. A case-sensitive comparison would refuse an honest
// release and, worse, push whoever is maintaining it towards skipping the check.
func TestVerifyAssetChecksumAcceptsUppercaseManifest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	digest := strings.ToUpper(hex.EncodeToString(fixtureDigest[:]))
	srv, _ := manifestServer(t, digest+"  "+fixtureAsset+"\n")

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	if err := verifyAssetChecksum(dir, asset, rel); err != nil {
		t.Fatalf("an uppercase digest must be accepted, got %v", err)
	}
}

// TestVerifyAssetChecksumRejectsMismatchedDigest is the core of the feature.
//
// Defect covered: this is the "unverified binary installed" case. Whatever is on
// disk is refused, the reason names the asset, and the message says the release
// was not installed rather than that something went wrong.
func TestVerifyAssetChecksumRejectsMismatchedDigest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	srv, _ := manifestServer(t, strings.Repeat("ab", 32)+"  "+fixtureAsset+"\n")

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	err := verifyAssetChecksum(dir, asset, rel)
	if err == nil {
		t.Fatal("a mismatched digest must be refused: this is the unverified binary install")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error must name the mismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), fixtureAsset) {
		t.Errorf("error must name the asset, got %v", err)
	}
}

// TestVerifyAssetChecksumRejectsManifestWithoutTheAsset: a manifest that does not
// list the binary must not be read as "nothing to check, so it is fine". That
// reading is what lets an attacker publish a manifest with a single line and
// have every asset accepted.
func TestVerifyAssetChecksumRejectsManifestWithoutTheAsset(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	// The digest of the asset is right there, for a different file.
	srv, _ := manifestServer(t, hex.EncodeToString(fixtureDigest[:])+"  nasbot-arm64\n")

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	err := verifyAssetChecksum(dir, asset, rel)
	if err == nil {
		t.Fatal("a manifest that does not list the asset must be refused")
	}
	if !strings.Contains(err.Error(), "does not list") {
		t.Errorf("error must say the manifest does not list the asset, got %v", err)
	}
}

// TestVerifyAssetChecksumRefusesReleaseWithoutManifest: no manifest URL at all.
// The release workflow publishes one next to every binary; a release that does
// not is either broken or not the release workflow, and either way it must not be
// installed.
func TestVerifyAssetChecksumRefusesReleaseWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	for _, checksums := range []string{"", "   ", "\n\t "} {
		rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset, ChecksumsURL: checksums}
		err := verifyAssetChecksum(dir, asset, rel)
		if err == nil {
			t.Fatalf("ChecksumsURL=%q must be refused", checksums)
		}
		if !strings.Contains(err.Error(), "refusing to install") {
			t.Errorf("error must say the install was refused, got %v", err)
		}
		if !strings.Contains(err.Error(), fixtureAsset) {
			t.Errorf("error must name the asset, got %v", err)
		}
	}
}

// TestVerifyAssetChecksumRejectsUnreachableManifest: the manifest download fails.
// It must not be read as "no digest to compare, so accept".
func TestVerifyAssetChecksumRejectsUnreachableManifest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("no manifest here"))
	}))
	t.Cleanup(srv.Close)

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	err := verifyAssetChecksum(dir, asset, rel)
	if err == nil {
		t.Fatal("an unreachable manifest must be refused")
	}
	if !strings.Contains(err.Error(), "cannot download") {
		t.Errorf("error must name the failed download, got %v", err)
	}
}

// TestVerifyAssetChecksumRejectsOversizedManifest: the manifest is capped at
// maxChecksumsSize. A hostile release answering with a gigabyte must be cut off
// rather than buffered.
func TestVerifyAssetChecksumRejectsOversizedManifest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	oversized := strings.Repeat("x", maxChecksumsSize+4096)
	srv, _ := manifestServer(t, hex.EncodeToString(fixtureDigest[:])+"  "+fixtureAsset+"\n"+oversized)

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	if err := verifyAssetChecksum(dir, asset, rel); err == nil {
		t.Fatalf("a manifest of %d bytes must exceed the %d byte cap and be refused",
			len(oversized), maxChecksumsSize)
	}
}

// TestVerifyAssetChecksumReportsMissingAsset: the file being verified is gone.
// The error must be about the read, not about the digest, or the operator will
// go looking in the wrong place.
func TestVerifyAssetChecksumReportsMissingAsset(t *testing.T) {
	dir := t.TempDir()

	srv, _ := manifestServer(t, hex.EncodeToString(fixtureDigest[:])+"  "+fixtureAsset+"\n")

	rel := releaseCandidate{Tag: "v9.9.9", AssetName: fixtureAsset,
		ChecksumsURL: srv.URL + "/" + checksumsAssetName}

	err := verifyAssetChecksum(dir, filepath.Join(dir, "never-written"), rel)
	if err == nil {
		t.Fatal("a missing asset must be refused")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error must wrap the missing file, got %v", err)
	}
}

// TestFileSHA256MatchesTheKnownDigest: the hash the comparison is built on. A
// truncation or an encoding slip here would make every comparison fail (safe) or
// make every digest equal (not safe).
func TestFileSHA256MatchesTheKnownDigest(t *testing.T) {
	dir := t.TempDir()
	asset := writeFixtureAsset(t, dir)

	got, err := fileSHA256(asset)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	if want := hex.EncodeToString(fixtureDigest[:]); got != want {
		t.Errorf("fileSHA256 = %q, want %q", got, want)
	}

	if _, err := fileSHA256(filepath.Join(dir, "absent")); err == nil {
		t.Error("fileSHA256 on a missing file must report an error")
	}
}

// TestDownloadToFileRefusesBadPayloads covers the download gate the verification
// sits behind, including the case a truncated release hits: Content-Length
// promises more bytes than arrive.
func TestDownloadToFileRefusesBadPayloads(t *testing.T) {
	cases := []struct {
		name string
		// wantErr lists the acceptable refusal messages: a truncated body can be
		// caught either by io.Copy reporting the error or by the written/announced
		// comparison, and both are correct answers.
		wantErr []string
		handler http.HandlerFunc
	}{
		{
			name: "empty payload",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "0")
				w.WriteHeader(http.StatusOK)
			},
			wantErr: []string{"empty payload"},
		},
		{
			name: "announced size over the cap",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(maxChecksumsSize+1))
				w.WriteHeader(http.StatusOK)
			},
			wantErr: []string{"exceeds"},
		},
		{
			name: "fewer bytes than announced",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "4096")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("short"))
			},
			wantErr: []string{"interrupted", "incomplete"},
		},
		{
			name: "more bytes than the cap",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				// No Content-Length: the body is cut, and only the copied count
				// reveals it.
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(strings.Repeat("x", maxChecksumsSize+16)))
			},
			wantErr: []string{"limit"},
		},
		{
			name: "non 2xx status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("missing"))
			},
			wantErr: []string{"status=404"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			dir := t.TempDir()
			path, err := downloadToFile(dir, srv.URL+"/payload", ".probe-*", maxChecksumsSize, checksumsTimeout)
			if err == nil {
				_ = os.Remove(path)
				t.Fatalf("expected a refusal, got %q", path)
			}
			matched := false
			for _, want := range tc.wantErr {
				if strings.Contains(err.Error(), want) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("error %q mentions none of %v", err, tc.wantErr)
			}
			// A refused download must leave nothing behind in the target
			// directory: the caller installs whatever it finds there.
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Errorf("a refused download left %d file(s) behind in %s", len(entries), dir)
			}
		})
	}
}

// TestDownloadToFileKeepsThePayloadOnSuccess is the control for the cases above:
// the same helper must still deliver a good payload, otherwise "refused" would be
// trivially satisfiable.
func TestDownloadToFileKeepsThePayloadOnSuccess(t *testing.T) {
	srv, _ := manifestServer(t, "payload body\n")
	dir := t.TempDir()

	path, err := downloadToFile(dir, srv.URL+"/"+checksumsAssetName, ".probe-*", maxChecksumsSize, checksumsTimeout)
	if err != nil {
		t.Fatalf("a valid payload must be accepted: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload body\n" {
		t.Errorf("payload = %q", got)
	}
	if !strings.HasPrefix(filepath.Base(path), ".probe-") {
		t.Errorf("temporary file %q does not carry the requested prefix", filepath.Base(path))
	}
}

// TestPickAssetURLOnlyResolvesPublishedAssets: ChecksumsURL is built from the
// release payload, so a name that is not published must resolve to the empty
// string and let verifyAssetChecksum refuse, not to a stale or guessed URL.
func TestPickAssetURLOnlyResolvesPublishedAssets(t *testing.T) {
	rel := githubRelease{Assets: []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{Name: "nasbot", BrowserDownloadURL: "http://example/nasbot"},
		{Name: checksumsAssetName, BrowserDownloadURL: "http://example/sums"},
	}}

	if got := pickAssetURL(rel, checksumsAssetName); got != "http://example/sums" {
		t.Errorf("pickAssetURL(%s) = %q", checksumsAssetName, got)
	}
	if got := pickAssetURL(rel, "not-published.txt"); got != "" {
		t.Errorf("pickAssetURL for an unpublished asset = %q, want empty", got)
	}

	name, url, ok := pickAsset(rel)
	if !ok || name != "nasbot" || url != "http://example/nasbot" {
		t.Errorf("pickAsset = %q, %q, %v", name, url, ok)
	}

	// An empty release publishes nothing downloadable, whatever the
	// architecture: falling back to a random asset would install a binary built
	// for the wrong CPU.
	empty := githubRelease{}
	if _, _, ok := pickAsset(empty); ok {
		t.Error("pickAsset must refuse a release with no assets")
	}
}
