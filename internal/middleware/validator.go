// Package middleware
// validator.go 注册自定义 validator 标签，供 DTO binding 使用。
// 对齐 shared/validation.ts 的 username / password 复杂度约束。
package middleware

import (
	"regexp"
	"sync"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var (
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	// 密码复杂度：至少一个小写 + 一个大写 + 一个数字，长度 8-72（长度由 min/max tag 管）
	pwLowerRe = regexp.MustCompile(`[a-z]`)
	pwUpperRe = regexp.MustCompile(`[A-Z]`)
	pwDigitRe = regexp.MustCompile(`\d`)

	registerOnce sync.Once
	registerErr  error
)

// RegisterCustomValidators 必须在应用启动早期调用一次。重复调用安全。
func RegisterCustomValidators() error {
	registerOnce.Do(func() {
		v, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			return
		}
		if err := v.RegisterValidation("username_chars", validateUsernameChars); err != nil {
			registerErr = err
			return
		}
		if err := v.RegisterValidation("password_complex", validatePasswordComplex); err != nil {
			registerErr = err
			return
		}
	})
	return registerErr
}

func validateUsernameChars(fl validator.FieldLevel) bool {
	return usernameRe.MatchString(fl.Field().String())
}

func validatePasswordComplex(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	return pwLowerRe.MatchString(s) && pwUpperRe.MatchString(s) && pwDigitRe.MatchString(s)
}
