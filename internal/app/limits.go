package app

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/shigabutdinoff/gophprofile/internal/avatar"
)

func readLimits() (size, proxies int, mimes []string, err error) {
	size, mimes = avatar.MaxSize, strings.Split(cmp.Or(os.Getenv("ALLOWED_MIME_TYPES"), "image/jpeg,image/png,image/webp"), ",")
	if v := os.Getenv("MAX_FILE_SIZE"); v != "" {
		if size, err = strconv.Atoi(v); err != nil || size < 1 || size > avatar.MaxSize {
			return 0, 0, nil, fmt.Errorf("неверный MAX_FILE_SIZE %q, нужно от 1 до %d байт", v, avatar.MaxSize)
		}
	}
	for _, m := range mimes {
		if f, ok := strings.CutPrefix(m, "image/"); !ok || !avatar.ValidFormat(f) {
			return 0, 0, nil, fmt.Errorf("неверный ALLOWED_MIME_TYPES, формат %q не поддерживается", m)
		}
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		if proxies, err = strconv.Atoi(v); err != nil || proxies < 0 {
			return 0, 0, nil, fmt.Errorf("неверный TRUSTED_PROXIES %q, нужно целое число от 0", v)
		}
	}
	return size, proxies, mimes, nil
}
