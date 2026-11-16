//go:build darwin

package main

// doneSoundVolume 是提示音的音量：系统的 Pop 原样放偏响，四成刚好听见。
const doneSoundVolume = 0.4

// playDoneSound 放系统自带的 Pop。macOS「播放用户界面音效」关掉时不放。
func playDoneSound() {
	withObjCPool(func() {
		if !uiSoundsEnabled() {
			return
		}
		s := objcSend(objcClass("NSSound"), "soundNamed:", uintptr(objcNSString("Pop")))
		if s == 0 {
			return
		}
		s.Send(objcSel("setVolume:"), float32(doneSoundVolume))
		objcSend(s, "stop") // 上一声还没放完时从头放
		objcSend(s, "play")
	})
}

// uiSoundsEnabled 读系统设置里的「播放用户界面音效」，没设过算开着。
func uiSoundsEnabled() bool {
	d := objcSend(objcClass("NSUserDefaults"), "standardUserDefaults")
	v := objcSend(d, "objectForKey:", uintptr(objcNSString("com.apple.sound.uiaudio.enabled")))
	return v == 0 || objcSendBool(v, "boolValue")
}
