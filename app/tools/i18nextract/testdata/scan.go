package sample

import (
 "fmt"
 "errors"
)

const title = "包常量"
var label = "包变量"
//go:embed 资源.txt
var asset string

type Row struct { Name string `json:"名字"` }

func screen(n int, name string, row Row, xs []string) string {
 // 中文注释不提取
 text := "你好"
 raw := `你好`
 _ = []string{"列表"}
 _ = fmt.Sprintf("已选 %d 个，%s，%v，100%%，{'原文'}", len(xs), name, row.Name)
 _ = fmt.Sprintf("重复 %s %s %s %s", name, row.Name, name, strings.TrimSpace(name))
 _ = fmt.Sprintf("宽度 %5d", n)
 _ = fmt.Sprintf("精度 %.1f", 1.2)
 _ = fmt.Sprintf("引号 %q", name)
 _ = fmt.Sprintf("十六 %x", n)
 _ = fmt.Sprintf("索引 %[1]d", n)
 _ = fmt.Sprintf("缺参 %d")
 _ = fmt.Sprintf("多参", n)
 _ = fmt.Sprintf("尾部 %")
 _ = fmt.Errorf("错误 %s", name)
 _ = errors.New("失败")
 if text == "相等" || text != "不等" { println("分支") }
 switch text { case "分支键": println("分支体") }
 _ = map[string]string{"映射键": "映射值"}
 _ = "前缀" + name
 _ = name + "后缀"
 Key("控件键")
 c.Key("实例键")
 //go:noinline
 println("指令旁")
 const local = "局部常量"
 _ = "全角！"
 _ = "标点。"
 _ = "ASCII"
 return "结束"
}
