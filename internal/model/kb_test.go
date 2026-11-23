package model

import "testing"

func TestKBRoleMapping(t *testing.T) {
	cases := []struct {
		role int16
		name string
		ok   bool
	}{
		{KBRoleOwner, "owner", true},
		{KBRoleEditor, "editor", true},
		{KBRoleMember, "member", true},
		{0, "", false},
		{9, "", false},
	}
	for _, tc := range cases {
		if got := KBRoleName(tc.role); got != tc.name {
			t.Fatalf("KBRoleName(%d)=%q，期望 %q", tc.role, got, tc.name)
		}
		role, ok := ParseKBRole(tc.name)
		if ok != tc.ok {
			t.Fatalf("ParseKBRole(%q) ok=%v，期望 %v", tc.name, ok, tc.ok)
		}
		if ok && role != tc.role {
			t.Fatalf("ParseKBRole(%q)=%d，期望 %d", tc.name, role, tc.role)
		}
	}
	if _, ok := ParseKBRole("admin"); ok {
		t.Fatal("admin 不是库内角色")
	}
}

func TestKBTableNames(t *testing.T) {
	if got := (KnowledgeBase{}).TableName(); got != "knowledge_bases" {
		t.Fatalf("KnowledgeBase 表名 %q", got)
	}
	if got := (KBMember{}).TableName(); got != "kb_members" {
		t.Fatalf("KBMember 表名 %q", got)
	}
	if got := (User{}).TableName(); got != "users" {
		t.Fatalf("User 表名 %q", got)
	}
	if got := (AuditLog{}).TableName(); got != "audit_logs" {
		t.Fatalf("AuditLog 表名 %q", got)
	}
	if got := (Setting{}).TableName(); got != "settings" {
		t.Fatalf("Setting 表名 %q", got)
	}
}
