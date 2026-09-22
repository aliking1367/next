package api

import (
	"errors"
	"net/http"

	settingsapp "github.com/aliking1367/next/internal/app/settings"
	userapp "github.com/aliking1367/next/internal/app/user"
)

// handleSubscriptionPageTemplates lists the bundled subscription page
// templates for the settings gallery.
func (s *Server) handleSubscriptionPageTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	names, err := settingsapp.SubscriptionPageTemplates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": names})
}

// handleSubscriptionPageTemplatePreview renders one bundled template with a
// demo user. The HTML goes back as JSON so the dashboard can place it in a
// sandboxed iframe; it never contains real user data.
func (s *Server) handleSubscriptionPageTemplatePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := r.URL.Query().Get("name")
	content, err := settingsapp.ReadSubscriptionPageTemplate(name)
	if err != nil {
		if errors.Is(err, settingsapp.ErrTemplateNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := userapp.RenderSubscriptionPagePreview(content)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "template error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "html": page})
}
