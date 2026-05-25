package config

import (
	"errors"
	"testing"
)

// TestPATPrefix_Const 校验三库统一前缀字面值，便于密钥扫描工具识别。
func TestPATPrefix_Const(t *testing.T) {
	if PATPrefix != "acc_pat_" {
		t.Errorf("PATPrefix = %q, want acc_pat_", PATPrefix)
	}
}

// TestValidatePATToken_OK 合法 token 应通过。
func TestValidatePATToken_OK(t *testing.T) {
	if err := ValidatePATToken("acc_pat_dev_alice_abcdef0123456789"); err != nil {
		t.Errorf("unexpected err: %v", err)
	}
}

// TestValidatePATToken_Empty 空 token → ErrPATEmpty。
func TestValidatePATToken_Empty(t *testing.T) {
	err := ValidatePATToken("")
	if !errors.Is(err, ErrPATEmpty) {
		t.Errorf("expected ErrPATEmpty, got %v", err)
	}
}

// TestValidatePATToken_BadPrefix 错误前缀 → ErrPATBadPrefix。
func TestValidatePATToken_BadPrefix(t *testing.T) {
	for _, tok := range []string{"wrong_xxx", "acc-pat-x", "AccPat_xxx", "acc_pat", "acc_pa_x"} {
		t.Run(tok, func(t *testing.T) {
			err := ValidatePATToken(tok)
			if !errors.Is(err, ErrPATBadPrefix) {
				t.Errorf("token %q expected ErrPATBadPrefix, got %v", tok, err)
			}
		})
	}
}

// TestValidatePATToken_Placeholder 含 PLACEHOLDER（大小写不敏感）→ ErrPATPlaceholder。
func TestValidatePATToken_Placeholder(t *testing.T) {
	for _, tok := range []string{
		"acc_pat_dev_alice_PLACEHOLDER000000",
		"acc_pat_placeholder_secret_value",
		"acc_pat_X_PlaceHolder_x",
	} {
		t.Run(tok, func(t *testing.T) {
			err := ValidatePATToken(tok)
			if !errors.Is(err, ErrPATPlaceholder) {
				t.Errorf("token %q expected ErrPATPlaceholder, got %v", tok, err)
			}
		})
	}
}

// TestValidatePATToken_PrefixCheckedBeforePlaceholder 错误前缀 + 含 PLACEHOLDER 时应先报前缀错（确定的错误顺序便于 caller 处理）。
func TestValidatePATToken_PrefixCheckedBeforePlaceholder(t *testing.T) {
	err := ValidatePATToken("wrong_PLACEHOLDER")
	if !errors.Is(err, ErrPATBadPrefix) {
		t.Errorf("expected ErrPATBadPrefix (prefix check first), got %v", err)
	}
}
