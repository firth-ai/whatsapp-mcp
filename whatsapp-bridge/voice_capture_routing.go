package main

// FIRTH FORK v2 · multi-company routing helpers.
//
// This file implements the routing layer that decides where each
// auto-downloaded media binary (audio or image) is written. Loaded by
// handleMessage via targetMediaPath().
//
// Contract (see specs/spec_voice_capture_via_whatsapp.md, Phase A Sub2):
//   - routing.json maps chat JID → company. Two schemas accepted:
//       flat:  {"<jid>": "<company_slug>"}
//       rich:  {"<jid>": {"company_slug": "...", "target_dir": "..."}}
//   - Precedence: self-chat → individual mapping → group participant → group default → _unmapped.
//   - Self-chat Mateo (81943748198555@lid) preserves the v1 legacy path
//     for the existing voice_capture pipeline (audios_raw/<msg_id>.ogg).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// VoiceCaptureRoute is the resolved destination for a mapped chat or
// participant.
type VoiceCaptureRoute struct {
	CompanySlug string `json:"company_slug"`
	TargetDir   string `json:"target_dir,omitempty"`
}

// selfChatJID is Mateo's "Mensajes a mí mismo" chat. Routing is bypassed
// for this JID to preserve the v1 contract (audios_raw + .chat_jid sidecar).
const selfChatJID = "81943748198555@lid"

// loadRoutingJSON reads VOICE_CAPTURE_ROUTING_JSON (path) and returns the
// chat → route map. Returns an empty map when:
//   - env var is empty or unset
//   - file does not exist
//   - file is malformed (warning logged via stderr)
func loadRoutingJSON(envVar string) map[string]VoiceCaptureRoute {
	out := map[string]VoiceCaptureRoute{}

	path := strings.TrimSpace(os.Getenv(envVar))
	if path == "" {
		return out
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[voice_capture] routing.json read failed at %s: %v\n", path, err)
		return out
	}

	// Try rich schema first (more structured).
	rich := map[string]VoiceCaptureRoute{}
	if err := json.Unmarshal(raw, &rich); err == nil {
		for jid, r := range rich {
			if r.CompanySlug == "" {
				// Probably flat-string entry; will be handled by fallback parse below.
				rich = nil
				break
			}
			if r.TargetDir == "" {
				r.TargetDir = defaultCompanyMediaDir(r.CompanySlug)
			}
			out[jid] = r
		}
		if len(out) > 0 {
			return out
		}
	}

	// Fallback: flat schema {"<jid>": "<company_slug>"}.
	flat := map[string]string{}
	if err := json.Unmarshal(raw, &flat); err != nil {
		fmt.Fprintf(os.Stderr, "[voice_capture] routing.json parse failed at %s: %v\n", path, err)
		return map[string]VoiceCaptureRoute{}
	}
	for jid, slug := range flat {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		out[jid] = VoiceCaptureRoute{
			CompanySlug: slug,
			TargetDir:   defaultCompanyMediaDir(slug),
		}
	}
	return out
}

// defaultCompanyMediaDir returns the canonical _media path for a company.
// Relative path; combined with repoRoot at write time.
func defaultCompanyMediaDir(slug string) string {
	return filepath.Join("documentation", "companies", slug, "canales", "whatsapp", "_media")
}

// routeMediaTarget resolves (companySlug, targetDir, mapped) for a given
// chat JID and sender JID. Precedence:
//  1. Individual chat (chat == sender) mapped → that company.
//  2. Group chat, participant mapped → participant's company.
//  3. Group chat, only group mapped → group's company.
//  4. Otherwise → "_unmapped" + warning dir.
//
// Note: self-chat is NOT handled here; callers that need legacy v1 path
// behavior (saveVoiceCaptureAudio) must check isSelfChat() upstream.
func routeMediaTarget(chatJID, senderJID string) (string, string, bool) {
	routes := loadRoutingJSON("VOICE_CAPTURE_ROUTING_JSON")

	isGroup := strings.HasSuffix(chatJID, "@g.us")

	if !isGroup {
		// Individual chat — key on chatJID.
		if r, ok := routes[chatJID]; ok {
			return r.CompanySlug, r.TargetDir, true
		}
	} else {
		// Group chat — try participant first, then group default.
		if senderJID != "" {
			if r, ok := routes[senderJID]; ok {
				return r.CompanySlug, r.TargetDir, true
			}
		}
		if r, ok := routes[chatJID]; ok {
			return r.CompanySlug, r.TargetDir, true
		}
	}

	// Unmapped fallback.
	return "_unmapped", defaultCompanyMediaDir("_unmapped"), false
}

// isSelfChat returns true if the JID is Mateo's self-chat. Triggers the
// v1 legacy path (audios_raw/, .chat_jid sidecar) for backwards
// compatibility with the Python pipeline's existing manifest.
func isSelfChat(chatJID string) bool {
	return chatJID == selfChatJID
}

// msgIDSanitizer extracts the alphanumeric prefix.
var msgIDSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeMsgID returns the first 8 lowercase alphanumeric characters of
// the WhatsApp message ID. Filenames must be safe across Windows/macOS/Linux.
func sanitizeMsgID(id string) string {
	if id == "" {
		return "unknown"
	}
	clean := msgIDSanitizer.ReplaceAllString(strings.ToLower(id), "")
	if clean == "" {
		return "unknown"
	}
	if len(clean) > 8 {
		clean = clean[:8]
	}
	return clean
}

// mediaExtensionFromType picks the right file extension. Audio is always
// .ogg (WhatsApp PTT); images dispatch by MIME ("image/png" → "png",
// otherwise "jpg").
func mediaExtensionFromType(mediaType, mime string) string {
	switch mediaType {
	case "audio":
		return "ogg"
	case "image":
		switch strings.ToLower(strings.TrimSpace(mime)) {
		case "image/png":
			return "png"
		case "image/jpeg", "image/jpg":
			return "jpg"
		default:
			return "jpg"
		}
	default:
		return "bin"
	}
}

// resolveRepoRoot returns VOICE_CAPTURE_REPO_ROOT or a heuristic fallback
// (5 levels up from cwd: bridge/../../../../../ = repo root).
func resolveRepoRoot() string {
	if r := strings.TrimSpace(os.Getenv("VOICE_CAPTURE_REPO_ROOT")); r != "" {
		return r
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	// Heuristic: bridge cwd → repo root is 4 levels up
	// (whatsapp-bridge/ → whatsapp-mcp/ → external/ → whatsapp_sync/ → automations/ → repo).
	root := cwd
	for i := 0; i < 5; i++ {
		root = filepath.Dir(root)
	}
	return root
}

// targetMediaPath returns the absolute path where the bridge will write
// the next media binary.
//
//   - Self-chat → <repoRoot>/automations/voice_capture/audios_raw/<msgID>.ogg (v1 legacy).
//   - Mapped chat → <repoRoot>/<targetDir>/<YYYY-MM-DD>_<HHMM>_<mediaType>_<msgID_8>.<ext>.
//   - Unmapped → <repoRoot>/documentation/companies/_unmapped/canales/whatsapp/_media/...
func targetMediaPath(chatJID, senderJID, mediaType, msgID, ext, repoRoot string, ts time.Time) string {
	if repoRoot == "" {
		repoRoot = resolveRepoRoot()
	}

	// v1 legacy path — preserve audios_raw contract for Mateo's bitácora.
	if isSelfChat(chatJID) {
		// Full msg_id (no truncation) to match v1 manifest keys.
		return filepath.Join(repoRoot, "automations", "voice_capture", "audios_raw", msgID+"."+ext)
	}

	_, dir, _ := routeMediaTarget(chatJID, senderJID)

	filename := fmt.Sprintf("%s_%s_%s_%s.%s",
		ts.Format("2006-01-02"),
		ts.Format("1504"),
		mediaType,
		sanitizeMsgID(msgID),
		ext,
	)
	return filepath.Join(repoRoot, dir, filename)
}
