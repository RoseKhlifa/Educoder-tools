package problem

import "testing"

func TestReadOnlyContextKeepsLegacyBankIdentityAndHashesExactDriver(t *testing.T) {
	legacy, err := Prepare(sample())
	if err != nil {
		t.Fatal(err)
	}
	in := sample()
	in.ReadOnlyFiles = []ReadOnlyFile{{Path: "242/driver.cpp", Language: "cpp", Content: "int main(){return 0;}\r\n"}}
	with, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	if with.RevisionHash != legacy.RevisionHash || with.ContractHash != legacy.ContractHash || with.ContextHash == "" || legacy.ContextHash != "" {
		t.Fatalf("read-only context changed bank identity or is not separately bound: %+v", with)
	}
	if with.ReadOnlyFiles[0].ContentHash != HashContent(in.ReadOnlyFiles[0].Content) || in.ReadOnlyFiles[0].ContentHash != "" {
		t.Fatal("exact source hash lost or caller mutated")
	}
	in.ReadOnlyFiles[0].Content = "int main(){return 1;}\n"
	changed, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ContextHash == with.ContextHash || changed.ContractHash != legacy.ContractHash {
		t.Fatal("driver change failed to separate generation context")
	}
}

func TestReadOnlyContextRejectsOverlapAndInvalidSource(t *testing.T) {
	for _, file := range []ReadOnlyFile{
		{Path: "242/main.cpp", Language: "cpp", Content: "driver"},
		{Path: "../main.cpp", Language: "cpp", Content: "driver"},
		{Path: "242/driver.cpp", Language: "cpp", Content: ""},
		{Path: "242/driver.cpp", Content: "driver"},
		{Path: "242/driver.cpp", Language: "cpp", Content: "Cookie: session=secret"},
	} {
		in := sample()
		in.ReadOnlyFiles = []ReadOnlyFile{file}
		if _, err := Prepare(in); err == nil {
			t.Errorf("invalid context accepted: %+v", file)
		}
	}
}
