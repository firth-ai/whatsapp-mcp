package main

// FIRTH FORK v2 · unit tests for multi-company routing.
//
// These tests cover the public API of:
//   - loadRoutingJSON(envVar string) map[string]VoiceCaptureRoute
//   - routeMediaTarget(chatJID, senderJID string) (companySlug, targetDir string, mapped bool)
//   - sanitizeMsgID(id string) string
//   - mediaExtensionFromType(mediaType, mime string) string
//   - targetMediaPath(chatJID, senderJID, mediaType, msgID, ext, repoRoot string, ts time.Time) string
//
// No network, no DB, no whatsmeow client — pure stdlib + reflection of router.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ----- loadRoutingJSON --------------------------------------------------------

func writeTempRouting(t *testing.T, payload interface{}) string {
	t.Helper()
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "routing.json")
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatalf("write routing.json: %v", err)
	}
	return p
}

func TestLoadRoutingJSON_EmptyEnvVarReturnsEmptyMap(t *testing.T) {
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", "")
	out := loadRoutingJSON("VOICE_CAPTURE_ROUTING_JSON")
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(out))
	}
}

func TestLoadRoutingJSON_NonExistentFileReturnsEmptyMap(t *testing.T) {
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", filepath.Join(t.TempDir(), "does_not_exist.json"))
	out := loadRoutingJSON("VOICE_CAPTURE_ROUTING_JSON")
	if len(out) != 0 {
		t.Fatalf("expected empty map for missing file, got %d", len(out))
	}
}

func TestLoadRoutingJSON_FlatSchema(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
		"120363ZZZ@g.us":           "zfb-irm-fmm",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	out := loadRoutingJSON("VOICE_CAPTURE_ROUTING_JSON")
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out["57301AAAA@s.whatsapp.net"].CompanySlug != "uniban" {
		t.Errorf("uniban not parsed: %+v", out["57301AAAA@s.whatsapp.net"])
	}
	if out["120363ZZZ@g.us"].CompanySlug != "zfb-irm-fmm" {
		t.Errorf("zfb not parsed: %+v", out["120363ZZZ@g.us"])
	}
	// target_dir auto-derived
	if !strings.Contains(out["57301AAAA@s.whatsapp.net"].TargetDir, "uniban") {
		t.Errorf("expected derived target_dir to contain 'uniban', got %q",
			out["57301AAAA@s.whatsapp.net"].TargetDir)
	}
}

func TestLoadRoutingJSON_RichSchema(t *testing.T) {
	path := writeTempRouting(t, map[string]map[string]string{
		"57301BBBB@s.whatsapp.net": {
			"company_slug": "uniban",
			"target_dir":   "documentation/companies/uniban/canales/whatsapp/_media",
		},
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	out := loadRoutingJSON("VOICE_CAPTURE_ROUTING_JSON")
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	got := out["57301BBBB@s.whatsapp.net"]
	if got.CompanySlug != "uniban" {
		t.Errorf("company_slug = %q, want uniban", got.CompanySlug)
	}
	if got.TargetDir != "documentation/companies/uniban/canales/whatsapp/_media" {
		t.Errorf("target_dir = %q", got.TargetDir)
	}
}

// ----- routeMediaTarget -------------------------------------------------------

func TestRouteMediaTarget_IndividualChatMapped(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	slug, dir, mapped := routeMediaTarget("57301AAAA@s.whatsapp.net", "57301AAAA@s.whatsapp.net")
	if !mapped {
		t.Fatalf("expected mapped=true")
	}
	if slug != "uniban" {
		t.Errorf("slug=%q, want uniban", slug)
	}
	if !strings.Contains(dir, "uniban") {
		t.Errorf("dir=%q does not contain 'uniban'", dir)
	}
}

func TestRouteMediaTarget_GroupWithMappedParticipant(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",         // participant
		"120363ZZZ@g.us":           "zfb-irm-fmm",    // group default
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	// Group chat, participant is mapped to uniban → routing must pick uniban
	slug, _, mapped := routeMediaTarget("120363ZZZ@g.us", "57301AAAA@s.whatsapp.net")
	if !mapped {
		t.Fatalf("expected mapped=true")
	}
	if slug != "uniban" {
		t.Errorf("expected participant-level routing (uniban), got %q", slug)
	}
}

func TestRouteMediaTarget_GroupParticipantNotMappedFallsBackToGroup(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"120363ZZZ@g.us": "zfb-irm-fmm",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	slug, _, mapped := routeMediaTarget("120363ZZZ@g.us", "57301UNKNOWN@s.whatsapp.net")
	if !mapped {
		t.Fatalf("expected group-level mapped=true")
	}
	if slug != "zfb-irm-fmm" {
		t.Errorf("expected group fallback (zfb-irm-fmm), got %q", slug)
	}
}

func TestRouteMediaTarget_UnmappedReturnsUnmappedFallback(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)

	slug, dir, mapped := routeMediaTarget("57399ZZZZ@s.whatsapp.net", "57399ZZZZ@s.whatsapp.net")
	if mapped {
		t.Fatalf("expected mapped=false for unmapped chat")
	}
	if slug != "_unmapped" {
		t.Errorf("expected slug=_unmapped, got %q", slug)
	}
	if !strings.Contains(dir, "_unmapped") {
		t.Errorf("expected dir to contain _unmapped, got %q", dir)
	}
}

// ----- sanitizeMsgID ----------------------------------------------------------

func TestSanitizeMsgID_TruncatesAndLowercases(t *testing.T) {
	got := sanitizeMsgID("ABCD1234EFGH5678LONGSUFFIX")
	if got != "abcd1234" {
		t.Errorf("got %q, want abcd1234", got)
	}
}

func TestSanitizeMsgID_StripsNonAlnum(t *testing.T) {
	got := sanitizeMsgID("AB-CD!12$34")
	// first 8 chars after strip should be "abcd1234"
	if got != "abcd1234" {
		t.Errorf("got %q, want abcd1234", got)
	}
}

func TestSanitizeMsgID_EmptyReturnsUnknown(t *testing.T) {
	got := sanitizeMsgID("")
	if got != "unknown" {
		t.Errorf("got %q, want 'unknown'", got)
	}
}

// ----- mediaExtensionFromType -------------------------------------------------

func TestMediaExtensionFromType_AudioAlwaysOgg(t *testing.T) {
	if got := mediaExtensionFromType("audio", "audio/ogg"); got != "ogg" {
		t.Errorf("got %q, want ogg", got)
	}
	if got := mediaExtensionFromType("audio", ""); got != "ogg" {
		t.Errorf("got %q, want ogg (fallback)", got)
	}
}

func TestMediaExtensionFromType_ImageDispatchesByMime(t *testing.T) {
	if got := mediaExtensionFromType("image", "image/png"); got != "png" {
		t.Errorf("png mime got %q", got)
	}
	if got := mediaExtensionFromType("image", "image/jpeg"); got != "jpg" {
		t.Errorf("jpeg mime got %q, want jpg", got)
	}
	if got := mediaExtensionFromType("image", ""); got != "jpg" {
		t.Errorf("image empty mime fallback got %q, want jpg", got)
	}
}

// ----- targetMediaPath --------------------------------------------------------

func TestTargetMediaPath_SelfChatLegacyPath(t *testing.T) {
	// Self-chat Mateo bypasses routing.json (legacy v1 contract).
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", "")
	repoRoot := t.TempDir()
	t.Setenv("VOICE_CAPTURE_REPO_ROOT", repoRoot)

	ts := time.Date(2026, 5, 24, 12, 32, 5, 0, time.UTC)
	got := targetMediaPath("81943748198555@lid", "81943748198555@lid", "audio", "MSG123", "ogg", repoRoot, ts)

	wantSuffix := filepath.Join("automations", "voice_capture", "audios_raw", "MSG123.ogg")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("got %q, expected suffix %q", got, wantSuffix)
	}
}

func TestTargetMediaPath_MappedChatProducesCanonicalCompanyDir(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)
	repoRoot := t.TempDir()
	t.Setenv("VOICE_CAPTURE_REPO_ROOT", repoRoot)

	ts := time.Date(2026, 5, 24, 12, 32, 5, 0, time.UTC)
	got := targetMediaPath("57301AAAA@s.whatsapp.net", "57301AAAA@s.whatsapp.net",
		"audio", "ABCD1234EFGH", "ogg", repoRoot, ts)

	// Expect: <repoRoot>/documentation/companies/uniban/canales/whatsapp/_media/2026-05-24_1232_audio_abcd1234.ogg
	wantSub := filepath.Join("documentation", "companies", "uniban", "canales", "whatsapp", "_media")
	if !strings.Contains(got, wantSub) {
		t.Errorf("got %q, expected to contain %q", got, wantSub)
	}
	if !strings.Contains(got, "2026-05-24_1232_audio_abcd1234.ogg") {
		t.Errorf("got %q, expected filename '2026-05-24_1232_audio_abcd1234.ogg'", got)
	}
}

func TestTargetMediaPath_UnmappedChatUsesUnmappedDir(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)
	repoRoot := t.TempDir()
	t.Setenv("VOICE_CAPTURE_REPO_ROOT", repoRoot)

	ts := time.Date(2026, 5, 24, 12, 32, 5, 0, time.UTC)
	got := targetMediaPath("57399UNKNOWN@s.whatsapp.net", "57399UNKNOWN@s.whatsapp.net",
		"image", "MSG", "png", repoRoot, ts)

	wantSub := filepath.Join("documentation", "companies", "_unmapped", "canales", "whatsapp", "_media")
	if !strings.Contains(got, wantSub) {
		t.Errorf("got %q, expected _unmapped path containing %q", got, wantSub)
	}
}

func TestTargetMediaPath_GroupRoutesByParticipant(t *testing.T) {
	path := writeTempRouting(t, map[string]string{
		"57301AAAA@s.whatsapp.net": "uniban",
		"120363ZZZ@g.us":           "zfb-irm-fmm",
	})
	t.Setenv("VOICE_CAPTURE_ROUTING_JSON", path)
	repoRoot := t.TempDir()
	t.Setenv("VOICE_CAPTURE_REPO_ROOT", repoRoot)

	ts := time.Date(2026, 5, 24, 12, 32, 5, 0, time.UTC)
	got := targetMediaPath("120363ZZZ@g.us", "57301AAAA@s.whatsapp.net",
		"audio", "MSG12345", "ogg", repoRoot, ts)

	if !strings.Contains(got, filepath.Join("companies", "uniban")) {
		t.Errorf("got %q, expected participant routing to uniban", got)
	}
}
