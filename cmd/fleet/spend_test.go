package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/spend"
)

func spendRows() ([]fleet.AgentSpend, spend.Spend) {
	one := spend.Spend{Calls: 3, Input: 200, Output: 1000, CacheRead: 55004, CacheWrite: 5000, AIC: 12.8, HasAIC: true}
	rows := []fleet.AgentSpend{
		{Name: "one", Profile: "copilot", Session: "SID", Known: true, Spend: one},
		{Name: "two", Profile: "agy", Session: "X"},
	}
	var total spend.Spend
	total.Add(one)
	return rows, total
}

func TestWriteSpendTable(t *testing.T) {
	rows, total := spendRows()
	var b bytes.Buffer
	if err := writeSpend(&b, rows, total, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"NAME", "CALLS", "TOKENS", "SPEND",
		"one", "61k", "12.8 AIC",
		"two", "no usage recorded",
		"TOTAL", fleet.SpendNote,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteSpendEmptyAndJSON(t *testing.T) {
	var b bytes.Buffer
	if err := writeSpend(&b, nil, spend.Spend{}, false); err != nil {
		t.Fatal(err)
	}
	if b.String() != "no agents\n" {
		t.Fatalf("empty: %q", b.String())
	}
	rows, total := spendRows()
	b.Reset()
	if err := writeSpend(&b, rows, total, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Agents []struct {
			Name  string `json:"name"`
			Known bool   `json:"known"`
			Calls int    `json:"calls"`
		} `json:"agents"`
		Total struct {
			AIC float64 `json:"aic"`
		} `json:"total"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != 2 || !got.Agents[0].Known || got.Agents[0].Calls != 3 || got.Agents[1].Known {
		t.Fatalf("agents %+v", got.Agents)
	}
	if got.Total.AIC != 12.8 || got.Note == "" {
		t.Fatalf("total %+v note %q", got.Total, got.Note)
	}
}
