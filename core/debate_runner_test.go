package core

import "testing"

func TestSelectDebateSpeakers(t *testing.T) {
	workers := []DebateRole{
		{Role: "jianzhu", DisplayName: "剑主"},
		{Role: "wendan", DisplayName: "文胆"},
		{Role: "xingzou", DisplayName: "行走"},
		{Role: "zhanggui", DisplayName: "掌柜"},
	}

	got1 := selectDebateSpeakers("host-decide", 1, workers, map[string]bool{}, 3)
	if len(got1) != 2 || got1[0].Role != "jianzhu" || got1[1].Role != "wendan" {
		t.Fatalf("round1 host-decide unexpected: %+v", got1)
	}

	spoken := map[string]bool{"jianzhu": true, "wendan": true}
	got2 := selectDebateSpeakers("cover-all-by-end", 3, workers, spoken, 3)
	if len(got2) < 2 {
		t.Fatalf("cover-all-by-end should prioritize uncovered roles: %+v", got2)
	}
}

func TestBuildRoleSessionKey(t *testing.T) {
	key := buildRoleSessionKey("feishu:oc_chat_xxx:ou_user", "oc_chat_xxx", "jianzhu")
	want := "feishu:oc_chat_xxx:debate_jianzhu"
	if key != want {
		t.Fatalf("session key mismatch: got %q want %q", key, want)
	}
}
