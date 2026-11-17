//go:build darwin

package main

func init() {
	// 与网页/设计稿的 -webkit-font-smoothing: antialiased 一致，关闭进程内 CoreText
	// 字体平滑，避免 Retina 屏下苹方中小字号被笔画膨胀、灰字发粗发糊。
	// 使用 NSRegistrationDomain，仅当前进程生效、不写入硬盘，且尊重系统用户已有覆盖。
	withObjCPool(func() {
		defaults := objcSend(objcClass("NSUserDefaults"), "standardUserDefaults")
		zero := objcSend(objcClass("NSNumber"), "numberWithInt:", 0)
		dict := objcSend(objcClass("NSDictionary"), "dictionaryWithObject:forKey:",
			uintptr(zero), uintptr(objcNSString("AppleFontSmoothing")))
		objcSend(defaults, "registerDefaults:", uintptr(dict))
	})
}
