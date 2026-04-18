// Copyright (C) 2026 Techdelight BV

package core

import "testing"

func TestParseBacklog_Empty(t *testing.T) {
	items := ParseBacklog("")
	if items != nil {
		t.Errorf("got %v, want nil", items)
	}
}

func TestParseBacklog_SingleItem(t *testing.T) {
	input := `# Backlog

| # | Item |
|---|------|
| 1 | Add user authentication |
`
	items := ParseBacklog(input)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Number != 1 {
		t.Errorf("Number = %d, want 1", items[0].Number)
	}
	if items[0].Description != "Add user authentication" {
		t.Errorf("Description = %q, want %q", items[0].Description, "Add user authentication")
	}
}

func TestParseBacklog_MultipleItems(t *testing.T) {
	input := `# Backlog

| # | Item |
|---|------|
| 1 | First item |
| 5 | Fifth item — with details |
| 12 | Twelfth item |
`
	items := ParseBacklog(input)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].Number != 1 {
		t.Errorf("items[0].Number = %d, want 1", items[0].Number)
	}
	if items[1].Number != 5 {
		t.Errorf("items[1].Number = %d, want 5", items[1].Number)
	}
	if items[1].Description != "Fifth item — with details" {
		t.Errorf("items[1].Description = %q", items[1].Description)
	}
	if items[2].Number != 12 {
		t.Errorf("items[2].Number = %d, want 12", items[2].Number)
	}
}

func TestParseBacklog_SkipsHeaderAndSeparator(t *testing.T) {
	input := `| # | Item |
|---|------|
| 1 | Real item |
`
	items := ParseBacklog(input)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Description != "Real item" {
		t.Errorf("Description = %q, want %q", items[0].Description, "Real item")
	}
}

func TestParseBacklog_NoTable(t *testing.T) {
	input := `# Backlog

Nothing here yet.
`
	items := ParseBacklog(input)
	if items != nil {
		t.Errorf("got %v, want nil", items)
	}
}
