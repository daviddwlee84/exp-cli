package research

import "testing"

func TestComputeBeliefDigestIsOrderIndependentAndEdgeSensitive(t *testing.T) {
	target := mustID(t, "fnd_01a01e60-0000-7001-8000-000000000001")
	first := BeliefInfluence{
		Source:   mustID(t, "fnd_01a01e60-0000-7002-8000-000000000002"),
		Relation: BeliefWeakens,
		Revision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	second := BeliefInfluence{
		Source:   mustID(t, "fnd_01a01e60-0000-7003-8000-000000000003"),
		Relation: BeliefOverturns,
		Revision: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	base := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	left, err := ComputeBeliefDigest(target, base, []BeliefInfluence{first, second})
	if err != nil {
		t.Fatal(err)
	}
	right, err := ComputeBeliefDigest(target, base, []BeliefInfluence{second, first})
	if err != nil || left != right {
		t.Fatalf("digest depends on edge order: %s != %s (%v)", left, right, err)
	}
	second.Relation = BeliefWeakens
	changed, err := ComputeBeliefDigest(target, base, []BeliefInfluence{first, second})
	if err != nil || changed == left {
		t.Fatalf("belief-changing relation did not change digest: %s (%v)", changed, err)
	}
}
