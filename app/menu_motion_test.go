package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMotionVisualSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出动效关键帧截图")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, tt := newTester(t)
	tt.SetScale(2)
	tt.Frame()
	must(t, tt.Click("看板"))
	tt.Advance(70 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-segmented-mid.png"), tt)
	tt.Advance(350 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-segmented-board.png"), tt)

	clickMenuButton(t, tt, "筛选")
	tt.Advance(55 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-menu-mid.png"), tt)
	tt.Advance(220 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-menu-settled.png"), tt)
}
