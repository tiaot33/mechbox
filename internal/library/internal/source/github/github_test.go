package github

import "testing"

func TestForgetDropsCachedObjectsAndCooldown(t *testing.T) {
	client := NewClient(nil, nil)
	key := memoKey(memoBlob, "owner", "repo", "sha")
	memoPut(&client.memo, &client.memo.blobs, key, []byte("from the mirror"))
	client.auth.anonymousReset.Store(1 << 40)
	client.Forget()
	if _, ok := memoGet(&client.memo, &client.memo.blobs, key); ok {
		t.Fatal("cached blob survived Forget")
	}
	if client.auth.anonymousReset.Load() != 0 {
		t.Fatal("rate limit cooldown survived Forget")
	}
	memoPut(&client.memo, &client.memo.blobs, key, []byte("direct"))
	if got, ok := memoGet(&client.memo, &client.memo.blobs, key); !ok || string(got) != "direct" {
		t.Fatalf("cache after Forget = %q, %v; want it still caching", got, ok)
	}
}
