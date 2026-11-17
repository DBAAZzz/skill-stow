//go:build !darwin

package main

import "github.com/egoist/mygo"

func installMainWindowZoom(*mygo.Window, mygo.WindowOptions) {}
