//go:build !darwin

package main

// playDoneSound 在其它平台上不放声音。
func playDoneSound() {}
