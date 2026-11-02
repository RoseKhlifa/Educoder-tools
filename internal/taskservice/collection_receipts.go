package taskservice

import (
	"encoding/json"
	"sort"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// Scores have no question revision, so a stable progress digest is useful only
// for a finite interval. A deep read periodically rechecks the actual content.
const collectionReceiptLifetime = 24 * time.Hour

func collectionFingerprint(hw educoder.Homework, progress *educoder.HomeworkProgress, current educoder.ChallengeProgress) string {
	type identity struct {
		ID             int64
		Name, Required string
		ST             *int
	}
	roster := make([]identity, 0, len(progress.Challenges))
	for _, p := range progress.Challenges {
		roster = append(roster, identity{p.ChallengeID, p.Name, p.RequiredStatus, p.ST})
	}
	sort.Slice(roster, func(i, j int) bool { return roster[i].ID < roster[j].ID })
	body, _ := json.Marshal(struct {
		Version               string
		HomeworkID            int64
		Name, Shixun, Runtime string
		StudentWorkID         int64
		Count                 int
		Roster                []identity
		Progress              educoder.ChallengeProgress
	}{"verified-collection-v1", hw.ID, hw.Name, hw.ShixunIdentifier, hw.MyShixunIdentifier, hw.StudentWorkID, hw.ChallengeCount, roster, current})
	return problem.HashContent(string(body))
}

func freshCollectionReceipts(hw educoder.Homework, progress *educoder.HomeworkProgress, receipts map[int64]questionbank.CollectionReceipt, now time.Time) map[int64]questionbank.CollectionReceipt {
	fresh := make(map[int64]questionbank.CollectionReceipt)
	for _, p := range progress.Challenges {
		r, ok := receipts[p.ChallengeID]
		if !ok || !p.HasFullScore() || r.VerifiedAt.After(now) || now.Sub(r.VerifiedAt) >= collectionReceiptLifetime || r.Fingerprint != collectionFingerprint(hw, progress, p) {
			continue
		}
		fresh[p.ChallengeID] = r
	}
	return fresh
}

func reuseCollectionReceipt(summary *CollectionSummary, receipt questionbank.CollectionReceipt) {
	summary.Levels++
	summary.ReusedLevels++
	summary.ReusedQuestions += receipt.Questions
	summary.Duplicates += receipt.Questions
	summary.Reasons["verified_checkpoint_reused"]++
}
