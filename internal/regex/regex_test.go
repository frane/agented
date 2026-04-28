package regex_test

import (
	"strings"
	"testing"

	"github.com/frane/agented/internal/regex"
)

func TestBasicMatch(t *testing.T) {
	content := "alpha\nbeta\nalphabet\n"
	matches, err := regex.Search("alpha", content, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Errorf("got %d matches, want 2", len(matches))
	}
	if matches[0].Line != 1 || matches[0].Column != 1 {
		t.Errorf("first match position: line=%d col=%d", matches[0].Line, matches[0].Column)
	}
	if matches[1].Line != 3 || matches[1].Column != 1 {
		t.Errorf("third-line match position: line=%d col=%d", matches[1].Line, matches[1].Column)
	}
}

func TestColumnOffset(t *testing.T) {
	content := "hello world\nfoo\n"
	matches, err := regex.Search("world", content, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Column != 7 {
		t.Errorf("got %+v", matches)
	}
}

func TestLimit(t *testing.T) {
	content := "x\nx\nx\nx\nx\n"
	matches, err := regex.Search("x", content, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Errorf("got %d", len(matches))
	}
}

func TestBadPattern(t *testing.T) {
	_, err := regex.Search("[unclosed", "abc", 0)
	if err == nil || !strings.Contains(err.Error(), "compile") {
		t.Errorf("expected compile error, got %v", err)
	}
}
