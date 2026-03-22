package core

import (
	"testing"
	"time"
)

func TestSquadStore_LatestRunIDForOwner(t *testing.T) {
	dir := t.TempDir()
	store := NewSquadStore(dir)
	owner := "feishu:oc_chat:ou_user"

	old := &SquadRun{
		RunID:           "squad_old",
		Status:          SquadStatusCompleted,
		Phase:           SquadPhaseCompleted,
		OwnerSessionKey: owner,
		RepoPath:        "/tmp/r",
		TaskPrompt:      "x",
		PlannerRole:     DefaultSquadPlannerRole,
		ExecutorRole:    DefaultSquadExecutorRole,
		ReviewerRole:    DefaultSquadReviewerRole,
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		UpdatedAt:       time.Now().Add(-2 * time.Hour),
		RoleRuntime:     map[string]SquadRoleRuntime{},
	}
	newer := &SquadRun{
		RunID:           "squad_newer",
		Status:          SquadStatusWaiting,
		Phase:           SquadPhaseWaitPlanApprove,
		OwnerSessionKey: owner,
		RepoPath:        "/tmp/r",
		TaskPrompt:      "y",
		PlannerRole:     DefaultSquadPlannerRole,
		ExecutorRole:    DefaultSquadExecutorRole,
		ReviewerRole:    DefaultSquadReviewerRole,
		CreatedAt:       time.Now().Add(-time.Hour),
		UpdatedAt:       time.Now().Add(-30 * time.Minute),
		RoleRuntime:     map[string]SquadRoleRuntime{},
	}
	other := &SquadRun{
		RunID:           "squad_other",
		Status:          SquadStatusRunning,
		Phase:           SquadPhasePlanning,
		OwnerSessionKey: "feishu:other:u",
		RepoPath:        "/tmp/r",
		TaskPrompt:      "z",
		PlannerRole:     DefaultSquadPlannerRole,
		ExecutorRole:    DefaultSquadExecutorRole,
		ReviewerRole:    DefaultSquadReviewerRole,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		RoleRuntime:     map[string]SquadRoleRuntime{},
	}
	for _, r := range []*SquadRun{old, newer, other} {
		if err := store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}

	id, err := store.LatestRunIDForOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	if id != "squad_newer" {
		t.Fatalf("want squad_newer, got %q", id)
	}

	empty, err := store.LatestRunIDForOwner("feishu:nobody:x")
	if err != nil {
		t.Fatal(err)
	}
	if empty != "" {
		t.Fatalf("want empty, got %q", empty)
	}
}

func TestSquadStore_LatestRunIDForOwner_Disabled(t *testing.T) {
	var store *SquadStore
	if id, err := store.LatestRunIDForOwner("k"); err != nil || id != "" {
		t.Fatalf("nil store: id=%q err=%v", id, err)
	}
	s := NewSquadStore("")
	if id, err := s.LatestRunIDForOwner("k"); err != nil || id != "" {
		t.Fatalf("empty root: id=%q err=%v", id, err)
	}
}
