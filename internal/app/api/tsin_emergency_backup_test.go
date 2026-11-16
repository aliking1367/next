package api

import "testing"

func TestCleanTSinBackupHostsKeepsOnlyCandidates(t *testing.T) {
	candidates := []tsinBackupCandidate{
		{ID: 479, Remark: "France", Tag: "auto-reality-vision"},
		{ID: 2, Remark: "Next", Tag: "auto-reality-vision"},
	}
	got := cleanTSinBackupHosts([]int64{479, 479, 999, 2}, candidates)
	if len(got) != 2 || got[0] != 2 || got[1] != 479 {
		t.Fatalf("got %v", got)
	}
	if got := cleanTSinBackupHosts(nil, candidates); len(got) != 0 {
		t.Fatalf("nil = %v", got)
	}
	if got := cleanTSinBackupHosts([]int64{479}, nil); len(got) != 0 {
		t.Fatalf("a host no longer in the service must be dropped: %v", got)
	}
}
