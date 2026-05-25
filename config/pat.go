package config

import (
	"errors"
	"fmt"
	"strings"
)

// PATPrefix 是所有用户访问令牌的固定前缀（"acc_pat_"），便于密钥扫描工具识别。
const PATPrefix = "acc_pat_"

// ValidatePATToken 返回的基础错误。consumer 应 wrap 加字段路径，例如：
//
//	if err := coreconfig.ValidatePATToken(p.Value); err != nil {
//	    return fmt.Errorf("auth.pats[%s]: %w", p.ID, err)
//	}
//
// 使用 errors.Is 在测试中按分类断言：
//
//	errors.Is(err, ErrPATBadPrefix)   // 是否前缀错
//	errors.Is(err, ErrPATPlaceholder) // 是否含 PLACEHOLDER
var (
	ErrPATEmpty       = errors.New("config: PAT token is empty")
	ErrPATBadPrefix   = fmt.Errorf("config: PAT token must start with %q", PATPrefix)
	ErrPATPlaceholder = errors.New("config: PAT token contains PLACEHOLDER; replace with a real token (env var injection recommended)")
)

// ValidatePATToken 校验单个 PAT 值的安全规则：
//   - 非空 → 否则 ErrPATEmpty
//   - 以 PATPrefix 开头 → 否则 ErrPATBadPrefix
//   - 不含 "placeholder"（大小写不敏感）→ 否则 ErrPATPlaceholder
//
// PLACEHOLDER 检查防示例配置中的占位令牌被直接部署到生产，等同默认凭据可被
// 远程利用（CLAUDE.md 11.1 安全底线）。
//
// 校验顺序：empty → prefix → placeholder。前缀错和 PLACEHOLDER 同时出现时
// 先报前缀错，便于 caller 处理首因。
func ValidatePATToken(token string) error {
	if token == "" {
		return ErrPATEmpty
	}
	if !strings.HasPrefix(token, PATPrefix) {
		return ErrPATBadPrefix
	}
	if strings.Contains(strings.ToLower(token), "placeholder") {
		return ErrPATPlaceholder
	}
	return nil
}
