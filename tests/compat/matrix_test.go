package compat

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCompatibilityMatrixTracksEveryPythonTest(t *testing.T) {
	python, err := os.ReadFile("../../tests/test_fleet.py")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := os.ReadFile("../compatibility-matrix.md")
	if err != nil {
		t.Fatal(err)
	}
	methods := regexp.MustCompile(`(?m)^    def (test_[a-zA-Z0-9_]+)\(`).FindAllStringSubmatch(string(python), -1)
	rows := regexp.MustCompile("(?m)^\\| `[^`]+\\.test_[^`]+` \\|").FindAllString(string(matrix), -1)
	if len(methods) != 48 || len(rows) != len(methods) {
		t.Fatalf("python=%d matrix=%d", len(methods), len(rows))
	}
	for _, match := range methods {
		if strings.Count(string(matrix), "."+match[1]+"` |") != 1 {
			t.Errorf("%s is missing or duplicated", match[1])
		}
	}
}
