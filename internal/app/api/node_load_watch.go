package api

import (
	"context"
	"fmt"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/nodecontroller"
)

// The node load watch reads every node's CPU and memory once a minute, the
// same numbers the nodes page shows, and tells the admin on Telegram when a
// node stays busy: users there slow down before anything fails, so the alert
// is the moment to move to a bigger server. It only reads.

const (
	nodeLoadEvery      = time.Minute
	nodeLoadHigh       = 80.0 // percent of CPU or memory that counts as busy
	nodeLoadNormal     = 70.0 // below this on both counts as calm again
	nodeLoadHighChecks = 5    // busy minutes in a row before an alert
	nodeLoadCalmChecks = 2    // calm minutes in a row before "back to normal"
	nodeLoadRemind     = 6 * time.Hour
)

type nodeLoadSample struct {
	Name     string
	CPU      float64
	Memory   float64
	Upload   uint64 // bytes per second
	Download uint64
}

// nodeLoadState is what the watch remembers about one node.
type nodeLoadState struct {
	busy, calm int
	alerted    bool
	alertedAt  time.Time
}

type nodeLoadWatch struct {
	nodes map[int64]*nodeLoadState
}

// observe takes one minute's sample of a node and says what to send: an
// alert, a reminder, a recovery, or nothing ("").
func (w *nodeLoadWatch) observe(id int64, sample nodeLoadSample, now time.Time) string {
	if w.nodes == nil {
		w.nodes = map[int64]*nodeLoadState{}
	}
	state := w.nodes[id]
	if state == nil {
		state = &nodeLoadState{}
		w.nodes[id] = state
	}
	busy := sample.CPU >= nodeLoadHigh || sample.Memory >= nodeLoadHigh
	calm := sample.CPU < nodeLoadNormal && sample.Memory < nodeLoadNormal
	switch {
	case busy:
		state.busy++
		state.calm = 0
	case calm:
		state.calm++
		state.busy = 0
	default:
		// Between the two thresholds nothing changes either way.
		state.calm = 0
	}
	switch {
	case !state.alerted && state.busy >= nodeLoadHighChecks:
		state.alerted, state.alertedAt = true, now
		return "busy"
	case state.alerted && busy && now.Sub(state.alertedAt) >= nodeLoadRemind:
		state.alertedAt = now
		return "still_busy"
	case state.alerted && state.calm >= nodeLoadCalmChecks:
		state.alerted = false
		return "calm"
	}
	return ""
}

// forget drops nodes that are gone or no longer report, so a node that comes
// back starts counting afresh.
func (w *nodeLoadWatch) forget(seen map[int64]bool) {
	for id := range w.nodes {
		if !seen[id] {
			delete(w.nodes, id)
		}
	}
}

func (s *Server) runNodeLoadWatch(ctx context.Context) {
	ticker := time.NewTicker(nodeLoadEvery)
	defer ticker.Stop()
	watch := &nodeLoadWatch{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkNodeLoad(ctx, watch)
		}
	}
}

func (s *Server) checkNodeLoad(ctx context.Context, watch *nodeLoadWatch) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Warnf(logging.ComponentNode, "node load watch failed: %v", recovered)
		}
	}()
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	base, err := s.nodeController.List(listCtx, nodecontroller.Request{IncludeMetrics: false})
	cancel()
	if err != nil {
		return
	}
	seen := map[int64]bool{}
	for _, node := range base.Nodes {
		if node.ID <= 0 || node.Status != "connected" {
			continue
		}
		getCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		item, err := s.nodeController.Get(getCtx, nodecontroller.Request{NodeID: node.ID})
		cancel()
		if err != nil || item.NodeServiceVersion == nil {
			continue // no metrics from it this minute
		}
		seen[node.ID] = true
		sample := nodeLoadSample{
			Name: item.Name, CPU: item.CPU.UsagePercent, Memory: item.Memory.UsagePercent,
			Upload: item.Transfer.UploadSpeed, Download: item.Transfer.DownloadSpeed,
		}
		if verdict := watch.observe(node.ID, sample, time.Now()); verdict != "" {
			title, lines := nodeLoadMessage(verdict, sample)
			s.telegramReports.NodeLoad(context.WithoutCancel(ctx), title, lines...)
		}
	}
	watch.forget(seen)
}

func nodeLoadMessage(verdict string, sample nodeLoadSample) (string, []string) {
	numbers := fmt.Sprintf("پردازنده: %.0f٪ · حافظه: %.0f٪ · ترافیک: ↓ %s ↑ %s",
		sample.CPU, sample.Memory, mbps(sample.Download), mbps(sample.Upload))
	switch verdict {
	case "busy":
		return "نود " + sample.Name + " زیر فشار است", []string{
			numbers,
			fmt.Sprintf("%d دقیقه پشت سر هم بالای %.0f٪ بوده؛ کاربرهای این نود ممکن است کندی ببینند.", nodeLoadHighChecks, nodeLoadHigh),
			"اگر ادامه دارد، وقت ارتقای این سرور است (هستهٔ پردازنده یا حافظهٔ بیشتر).",
		}
	case "still_busy":
		return "نود " + sample.Name + " هنوز زیر فشار است", []string{numbers}
	}
	return "نود " + sample.Name + " عادی شد", []string{numbers}
}

func mbps(bytesPerSecond uint64) string {
	return fmt.Sprintf("%.0f Mbps", float64(bytesPerSecond)*8/1_000_000)
}
