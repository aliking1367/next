package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	nodeapp "github.com/aliking1367/next/internal/app/node"
	telegramapp "github.com/aliking1367/next/internal/app/telegram"
)

func (s *Server) handleNodeRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/node" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload nodeapp.NodeCreate
	if err := decodeOptionalJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	node, err := s.nodeMutations.CreateNode(ctx, payload)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	s.telegramReports.NodeCreated(r.Context(), telegramNodeReport(node, "", telegramActor(r)))
	// A new node has no CDN hostname yet, so this is what turns adding a server
	// into one action instead of also editing DNS and hosts by hand.
	s.kickCDNAutomation("node created")
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleNodeSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	settings, err := s.nodeMutations.Settings(ctx)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleNodeCertificateNew(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pending, err := s.nodeMutations.CreatePendingCertificate(ctx, nodeapp.DefaultPendingCertificateTTL)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate":       pending.Certificate,
		"certificate_key":   pending.CertificateKey,
		"certificate_token": pending.Token,
	})
}

func (s *Server) handleNodeUpdate(w http.ResponseWriter, r *http.Request, nodeID int64) {
	var payload nodeapp.NodeModify
	if err := decodeOptionalJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	before, _ := s.nodeMutations.GetNode(ctx, nodeID)
	node, err := s.nodeMutations.UpdateNode(ctx, nodeID, payload)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	if strings.TrimSpace(before.Status) != "" && before.Status != node.Status {
		s.telegramReports.NodeStatusChanged(r.Context(), telegramNodeReport(node, before.Status, telegramActor(r)))
	}
	// Swapping a blocked server for a fresh one is an address change and a name
	// change, and both have to reach DNS or every user's config keeps pointing
	// at the server that stopped working.
	if before.Address != node.Address || before.Name != node.Name {
		s.kickCDNAutomation("node address or name changed")
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request, nodeID int64) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	before, _ := s.nodeMutations.GetNode(ctx, nodeID)
	// Read the hostname while the row still exists; once the node is gone there
	// is nothing left to look it up from.
	retiring := s.nodeCDNHostname(ctx, nodeID)
	if err := s.nodeMutations.DeleteNode(ctx, nodeID); err != nil {
		writeNodeMutationError(w, err)
		return
	}
	s.telegramReports.NodeDeleted(r.Context(), telegramNodeReport(before, "", telegramActor(r)))
	s.retireCDNForNode(retiring)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleNodeCertificateRegenerate(w http.ResponseWriter, r *http.Request, nodeID int64) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	node, err := s.nodeMutations.RegenerateNodeCertificate(ctx, nodeID)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleNodeUsageReset(w http.ResponseWriter, r *http.Request, nodeID int64) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	node, err := s.nodeMutations.ResetNodeUsage(ctx, nodeID)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	s.telegramReports.NodeUsageReset(r.Context(), telegramNodeReport(node, "", telegramActor(r)))
	writeJSON(w, http.StatusOK, node)
}

func telegramNodeReport(node nodeapp.NodeResponse, previousStatus string, actor string) telegramapp.NodeReport {
	return telegramapp.NodeReport{
		Name:             firstNonEmpty(node.Name, "node"),
		Address:          node.Address,
		APIPort:          node.APIPort,
		UsageCoefficient: node.UsageCoefficient,
		DataLimit:        node.DataLimit,
		Status:           node.Status,
		PreviousStatus:   previousStatus,
		Message:          ptrStringText(node.Message),
		Actor:            actor,
	}
}

func writeNodeMutationError(w http.ResponseWriter, err error) {
	switch {
	case nodeapp.IsKind(err, nodeapp.ErrorNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case nodeapp.IsKind(err, nodeapp.ErrorConflict):
		writeError(w, http.StatusConflict, err.Error())
	case nodeapp.IsKind(err, nodeapp.ErrorInvalid), nodeapp.IsKind(err, nodeapp.ErrorExpired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
