package model

import "testing"

func TestModelPurposeMapping(t *testing.T) {
	cases := []struct {
		p    int16
		name string
		ok   bool
	}{
		{ModelPurposeChat, "chat", true},
		{ModelPurposeEmbedding, "embedding", true},
		{ModelPurposeRerank, "rerank", true},
		{0, "", false},
		{9, "", false},
	}
	for _, tc := range cases {
		if got := ModelPurposeName(tc.p); got != tc.name {
			t.Fatalf("ModelPurposeName(%d)=%q，期望 %q", tc.p, got, tc.name)
		}
		p, ok := ParseModelPurpose(tc.name)
		if ok != tc.ok {
			t.Fatalf("ParseModelPurpose(%q) ok=%v，期望 %v", tc.name, ok, tc.ok)
		}
		if ok && p != tc.p {
			t.Fatalf("ParseModelPurpose(%q)=%d，期望 %d", tc.name, p, tc.p)
		}
	}
	if _, ok := ParseModelPurpose("vision"); ok {
		t.Fatal("vision 不是合法用途")
	}
}

func TestModelCfgTableNames(t *testing.T) {
	if got := (ModelService{}).TableName(); got != "model_services" {
		t.Fatalf("ModelService 表名 %q", got)
	}
	if got := (ModelEntry{}).TableName(); got != "model_entries" {
		t.Fatalf("ModelEntry 表名 %q", got)
	}
}
