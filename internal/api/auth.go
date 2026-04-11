package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OTPSender sends OTP codes to users. Implemented by *messenger.WhatsApp.
// Uses group message as fallback since WhatsApp linked devices can't DM
// their own phone number.
type OTPSender interface {
	SendDM(phone, text string) error
	Write(text string) error // group message fallback
}

// AuthHandler holds dependencies for the OTP auth endpoints.
type AuthHandler struct {
	OTP          *OTPStore
	DM           OTPSender         // nil in terminal mode — OTP disabled
	Allowlist    map[string]string // phone → name (from personas)
	JWTSecret    []byte
	DashboardURL string // e.g. "https://whatsapp-nagger.fly.dev"
}

// GenerateMagicLink creates a one-tap login URL for the given phone by
// generating an OTP server-side and embedding it in the URL query string.
// Implements agent.DashboardLinker.
func (ah *AuthHandler) GenerateMagicLink(phone string) (string, error) {
	if _, ok := ah.Allowlist[phone]; !ok {
		return "", fmt.Errorf("phone not in allowlist")
	}
	code, err := ah.OTP.Generate(phone)
	if err != nil {
		return "", err
	}
	base := ah.DashboardURL
	if base == "" {
		base = "https://whatsapp-nagger.fly.dev"
	}
	return fmt.Sprintf("%s/login?phone=%s&code=%s", base, phone, code), nil
}

// RegisterAuthRoutes mounts POST /api/auth/otp and POST /api/auth/verify.
func (ah *AuthHandler) RegisterAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/otp", ah.handleOTP)
	mux.HandleFunc("/api/auth/verify", ah.handleVerify)
}

// POST /api/auth/otp  {"phone": "972..."}
func (ah *AuthHandler) handleOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Phone == "" {
		http.Error(w, "phone is required", http.StatusBadRequest)
		return
	}

	// Check allowlist.
	if _, ok := ah.Allowlist[req.Phone]; !ok {
		http.Error(w, "phone not authorized", http.StatusForbidden)
		return
	}

	if ah.DM == nil {
		http.Error(w, "OTP auth not available (terminal mode)", http.StatusServiceUnavailable)
		return
	}

	code, err := ah.OTP.Generate(req.Phone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}

	name := ah.Allowlist[req.Phone]
	otpMsg := fmt.Sprintf("Dashboard code for %s: %s\nExpires in 5 minutes.", name, code)

	// Send via group message. DMs don't work when the bot is linked to the
	// same phone as the requester (WhatsApp silently drops them).
	if err := ah.DM.Write(otpMsg); err != nil {
		http.Error(w, "failed to send OTP: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"expires_in": 300,
	})
}

// POST /api/auth/verify  {"phone": "972...", "code": "123456"}
func (ah *AuthHandler) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	name, ok := ah.Allowlist[req.Phone]
	if !ok {
		http.Error(w, "phone not authorized", http.StatusForbidden)
		return
	}

	valid, err := ah.OTP.Verify(req.Phone, req.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !valid {
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}

	// Issue JWT.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   req.Phone,
		"name":  name,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(365 * 24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(ah.JWTSecret)
	if err != nil {
		http.Error(w, "token signing failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":    true,
		"name":  name,
		"token": signed,
	})
}

// BuildAllowlist creates a phone → name map from personas.md content.
// Reuses the same format as the agent's parsePersonaPhones.
func BuildAllowlist(personas string) map[string]string {
	// Parse "## Name" headers + "**Phone:** 972..." lines.
	// This duplicates agent.go's parsePersonaPhones but avoids a cross-package
	// import. The personas file is small and stable.
	import_regexp_inline()
	return parsePhones(personas)
}

// We need regexp for parsing — imported at package level would be cleaner
// but let's keep it self-contained to avoid confusion.
func init() {}

func parsePhones(personas string) map[string]string {
	phones := make(map[string]string)
	// Simple line-by-line parse instead of importing regexp.
	var currentName string
	for _, line := range splitLines(personas) {
		if len(line) > 3 && line[:3] == "## " {
			currentName = trimSpace(line[3:])
		}
		if currentName != "" {
			if idx := indexOf(line, "**Phone:**"); idx >= 0 {
				phone := trimSpace(line[idx+10:])
				if phone != "" {
					phones[phone] = currentName
				}
			}
		}
	}
	return phones
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func import_regexp_inline() {} // placeholder — we use manual parsing instead

// LoadPersonasFile reads the personas file from disk (same logic as agent).
func LoadPersonasFile() string {
	path := os.Getenv("PERSONAS_FILE")
	if path == "" {
		path = "personas.md"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
