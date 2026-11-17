package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func boardPanTester(project bool, columns int) (*app, *ui.Tester) {
	a := newApp(library.Sample())
	tt := newUITester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			content := func(width float32) {
				for i := range columns {
					header := func() { ui.Text(c, fmt.Sprintf("Column %d", i)) }
					body := func() { ui.Button(c, fmt.Sprintf("Card %d", i)) }
					if project {
						fitColumn(c.Key(i), header, body)
					} else {
						boardColumn(c.Key(i), width, header, body)
					}
				}
			}
			if project {
				a.fitBoard(c, func() { content(fitColumnWidth) })
			} else {
				a.boardRow(c, columns, 560, content)
			}
		})
	}, 560, 360)
	return a, tt
}

func TestBoardPanKeepsTouchpadNavigation(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprint(project), func(t *testing.T) {
			_, tt := boardPanTester(project, 4)
			before := boardHeaderX(t, tt)
			tt.Scroll(400, 250, 100, 0)
			if got := boardHeaderX(t, tt); got != before-100 {
				t.Fatalf("touchpad did not scroll horizontally: %v -> %v", before, got)
			}
			tt.Press(400, 5)
			tt.Move(500, 5)
			tt.Release(500, 5)
			if got := boardHeaderX(t, tt); got != before {
				t.Fatal("dragging back must start at the wheel/touchpad position")
			}
		})
	}
}

func TestBoardPanIgnoresJitterAndSecondaryButton(t *testing.T) {
	a, tt := boardPanTester(false, 4)
	before := boardHeaderX(t, tt)
	a.selected = a.lib.Skills[0]
	tt.Press(400, 5)
	tt.Move(398, 6)
	if got := boardHeaderX(t, tt); got != before {
		t.Fatal("small pointer jitter must not move the board")
	}
	tt.Release(398, 6)
	if a.selected != nil {
		t.Fatal("small pointer jitter should remain a blank click")
	}
	tt.PressButton(1, 400, 5)
	tt.Move(300, 5)
	tt.ReleaseButton(1, 300, 5)
	if got := boardHeaderX(t, tt); got != before {
		t.Fatal("secondary button must not pan")
	}
}

func TestBoardVerticalBlankDragPreservesSelection(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, dx := range []float32{0, 2} {
			t.Run(fmt.Sprintf("project=%v/dx=%v", project, dx), func(t *testing.T) {
				a, tt := boardPanTester(project, 4)
				selected := a.lib.Skills[0]
				a.selected = selected
				before := boardHeaderX(t, tt)
				tt.Press(400, 5)
				tt.Move(400+dx, 150)
				tt.Release(400+dx, 150)
				if a.selected != selected {
					t.Fatal("vertical blank drag must not become a deselect click")
				}
				if got := boardHeaderX(t, tt); got != before {
					t.Fatal("vertical drag with small horizontal jitter must not pan")
				}
			})
		}
	}
}

func TestBoardPanEndsOutsideAndOnCancel(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			a, tt := boardPanTester(false, 4)
			selected := a.lib.Skills[0]
			a.selected = selected
			start := boardHeaderX(t, tt)
			tt.Press(400, 5)
			tt.Move(-100, 5)
			if cancel {
				tt.CancelPress()
			} else {
				tt.Release(-100, 5)
			}
			if a.selected != selected {
				t.Fatal("ending a pan must preserve selection")
			}
			tt.Move(400, 5)
			if tt.Cursor() != ui.CursorGrab {
				t.Fatal("released/canceled pan must not retain grabbing cursor")
			}
			tt.Press(100, 5)
			tt.Move(550, 5)
			tt.Release(550, 5)
			if got := boardHeaderX(t, tt); got != start {
				t.Fatalf("dragging past the start should clamp at the start: %v", got)
			}
		})
	}
}

func TestBoardWithoutOverflowHasNoPanCursor(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprint(project), func(t *testing.T) {
			_, tt := boardPanTester(project, 1)
			tt.Move(400, 250)
			if tt.Cursor() == ui.CursorGrab || tt.Cursor() == ui.CursorGrabbing {
				t.Fatal("non-overflow board should not offer panning")
			}
		})
	}
}

func TestBoardPanDoesNotTakeCardPressesOrColumnScrolling(t *testing.T) {
	a := newApp(library.Sample())
	a.mode = viewBoard
	tt := newUITester(a.view, 1280, 800)
	r, ok := tt.Find("test-first")
	if !ok {
		t.Fatal("sample card missing")
	}
	tt.Move(r.X+r.W/2, r.Y-20)
	if tt.Cursor() != ui.CursorGrab {
		t.Fatal("the sample board must overflow for this test to mean anything")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Move(x, y)
	if tt.Cursor() != ui.CursorPointer {
		t.Fatal("cards should retain their pointer cursor")
	}
	tt.Press(x, y)
	tt.Move(x-40, y)
	tt.Release(x-40, y)
	if after, ok := tt.Find("test-first"); !ok || after.X != r.X {
		t.Fatal("dragging from a card must not pan the board")
	}
	must(t, tt.Click("test-first"))
	if a.selected == nil || a.selected.Name != "test-first" {
		t.Fatal("card click should still select the skill")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(paneDuration)
	r, _ = tt.Find("test-first")
	tt.Scroll(r.X+10, r.Y+10, 0, 80)
	if after, ok := tt.Find("test-first"); !ok || after.X != r.X || after.Y >= r.Y {
		t.Fatalf("ordinary scrolling must move column contents vertically: before=%v after=%v", r, after)
	}
}

func boardHeaderX(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	r, ok := tt.Find("Column 1")
	if !ok {
		t.Fatal("second column should be visible")
	}
	return r.X
}

func TestBoardBlankSpacePans(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, point := range []ui.Point{{X: 400, Y: 5}, {X: 247, Y: 250}, {X: 400, Y: 250}} {
			t.Run(fmt.Sprintf("project=%v/point=%v", project, point), func(t *testing.T) {
				a, tt := boardPanTester(project, 4)
				selected := a.lib.Skills[0]
				a.selected = selected
				before := boardHeaderX(t, tt)
				tt.Move(point.X, point.Y)
				if tt.Cursor() != ui.CursorGrab {
					t.Errorf("blank overflow area cursor = %v, want grab", tt.Cursor())
				}
				tt.Press(point.X, point.Y)
				tt.Move(point.X-3, point.Y) // 还在点击阈值内，越过后要补上这 3 DIP
				tt.Move(point.X-100, point.Y+15)
				if got := boardHeaderX(t, tt); got != before-100 {
					t.Errorf("drag should move columns left by 100: x=%v, before=%v", got, before)
				}
				if tt.Cursor() != ui.CursorGrabbing {
					t.Errorf("drag cursor = %v, want grabbing", tt.Cursor())
				}
				tt.Release(point.X-100, point.Y+15)
				if a.selected != selected {
					t.Fatal("panning must preserve selection")
				}
				// A later ordinary blank click still clears the selection.
				tt.Press(400, 5)
				tt.Release(400, 5)
				if a.selected != nil {
					t.Fatal("blank click should still deselect")
				}
			})
		}
	}
}
