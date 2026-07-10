package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

const sessionCookieName = "memos_importer_session"

type createSessionRequest struct {
	Password string `json:"password"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AccessPassword == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
		return
	}
	var req createSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !sameSecret(req.Password, s.cfg.AccessPassword) {
		writeError(w, http.StatusUnauthorized, errString("invalid access password"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken(s.cfg.AccessPassword),
		Path:     "/",
		MaxAge:   24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func isSessionCreate(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/session"
}

func (s *Server) hasValidCredential(r *http.Request) bool {
	if sameSecret(r.Header.Get("X-Memos-Importer-Password"), s.cfg.AccessPassword) {
		return true
	}
	if sameSecret(bearerToken(r.Header.Get("Authorization")), s.cfg.AccessPassword) {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return sameSecret(cookie.Value, sessionToken(s.cfg.AccessPassword))
}

func sessionToken(password string) string {
	mac := hmac.New(sha256.New, []byte(password))
	_, _ = mac.Write([]byte("memos-importer access session v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func sameSecret(got, want string) bool {
	if got == "" || want == "" {
		return got == want
	}
	return hmac.Equal([]byte(got), []byte(want))
}
