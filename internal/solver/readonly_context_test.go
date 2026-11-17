package solver

import (
	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"testing"
)

func TestGenerationKeySeparatesReadOnlyDriverEvenWithSameLegacyBankIdentity(t *testing.T) {
	one := testSnapshot()
	one.ReadOnlyFiles = []problem.ReadOnlyFile{{Path: "driver.cpp", Language: "cpp", Content: "int main(){return 0;}"}}
	two := one
	two.ReadOnlyFiles = []problem.ReadOnlyFile{{Path: "driver.cpp", Language: "cpp", Content: "int main(){return 1;}"}}
	if generationKey(answer.Request{Problem: one}) == generationKey(answer.Request{Problem: two}) {
		t.Fatal("different drivers shared generation dedupe")
	}
}
