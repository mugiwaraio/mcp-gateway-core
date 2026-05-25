package config

import (
	"strings"
	"testing"
)

// TestExpandEnvVars_Replaces 单变量正常替换 + missing 为空。
func TestExpandEnvVars_Replaces(t *testing.T) {
	t.Setenv("FOO", "value")
	out, missing := ExpandEnvVars("key: ${FOO}")
	if out != "key: value" {
		t.Errorf("out = %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v", missing)
	}
}

// TestExpandEnvVars_MissingTracked 未注入变量 → 替空 + 入 missing。
func TestExpandEnvVars_MissingTracked(t *testing.T) {
	out, missing := ExpandEnvVars("key: ${UNSET_VAR_FOR_TEST_X}")
	if out != "key: " {
		t.Errorf("out = %q", out)
	}
	if len(missing) != 1 || missing[0] != "UNSET_VAR_FOR_TEST_X" {
		t.Errorf("missing = %v", missing)
	}
}

// TestExpandEnvVars_MissingDedup 同一变量多次出现 → missing 去重保序。
func TestExpandEnvVars_MissingDedup(t *testing.T) {
	src := "a: ${UNSET_DEDUP_X}\nb: ${UNSET_DEDUP_X}\nc: ${UNSET_DEDUP_Y}\nd: ${UNSET_DEDUP_X}"
	_, missing := ExpandEnvVars(src)
	if len(missing) != 2 || missing[0] != "UNSET_DEDUP_X" || missing[1] != "UNSET_DEDUP_Y" {
		t.Errorf("missing = %v, want [UNSET_DEDUP_X UNSET_DEDUP_Y]", missing)
	}
}

// TestExpandEnvVars_CommentLineIgnored 整行注释中的 ${VAR} 不计入 missing。
func TestExpandEnvVars_CommentLineIgnored(t *testing.T) {
	src := "# example: ${UNSET_COMMENT_VAR}"
	_, missing := ExpandEnvVars(src)
	if len(missing) != 0 {
		t.Errorf("comment-line var should not be in missing: %v", missing)
	}
}

// TestExpandEnvVars_InlineCommentRecognized 行尾注释中的 ${VAR} 不计入 missing。
func TestExpandEnvVars_InlineCommentRecognized(t *testing.T) {
	src := "key: foo  # see ${UNSET_INLINE_VAR}"
	_, missing := ExpandEnvVars(src)
	if len(missing) != 0 {
		t.Errorf("inline-comment var should not be in missing: %v", missing)
	}
}

// TestExpandEnvVars_QuotedHashNotComment 引号内的 # 不开启注释。
func TestExpandEnvVars_QuotedHashNotComment(t *testing.T) {
	t.Setenv("INSIDE", "x")
	// 引号内 # 不是注释开始；后续的 ${INSIDE} 仍在代码区
	src := `key: "a#b ${INSIDE}"`
	out, missing := ExpandEnvVars(src)
	if !strings.Contains(out, `"a#b x"`) {
		t.Errorf("expected expansion inside quotes, got %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("missing should be empty: %v", missing)
	}
}

// TestExpandEnvVars_EmptyEnvIsMissing 显式 setenv="" 也视为未注入。
func TestExpandEnvVars_EmptyEnvIsMissing(t *testing.T) {
	t.Setenv("EMPTY_VAR_X", "")
	out, missing := ExpandEnvVars("key: ${EMPTY_VAR_X}")
	if out != "key: " {
		t.Errorf("out = %q", out)
	}
	if len(missing) != 1 || missing[0] != "EMPTY_VAR_X" {
		t.Errorf("missing = %v", missing)
	}
}

// TestEnvVarPattern_Exported 公开 regex 可被 caller 拿来做自定义校验。
func TestEnvVarPattern_Exported(t *testing.T) {
	if EnvVarPattern == nil {
		t.Fatal("EnvVarPattern is nil")
	}
	if !EnvVarPattern.MatchString("${FOO}") {
		t.Error("should match ${FOO}")
	}
	if EnvVarPattern.MatchString("${1FOO}") {
		t.Error("should not match ${1FOO} (must start with letter or _)")
	}
}
