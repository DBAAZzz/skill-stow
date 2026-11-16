package sample

import (
 "errors"
 "fmt"
)

var packageErr = errors.New("包级错误")
var packageFmt = fmt.Errorf("包级格式 %d", 1)

func messages(n int, name string, err error) {
 _ = errors.New("普通错误")
 _ = fmt.Errorf("错误 %d %s", n, name)
 _ = fmt.Errorf("错误 %d: %w", n, err)
 _ = fmt.Errorf("错误 %w 后文", err)
 _ = fmt.Errorf("错误 %w: %w", err, err)
 _ = fmt.Errorf("错误 %q: %w", name, err)
 _ = fmt.Errorf("错误: %w")
 _ = fmt.Errorf("错误 %%w")
}
