//go:build !darwin

package main

import (
	"github.com/egoist/mygo"

	"skillmanager/locales"
)

func openImportDialog(parent *mygo.Window, opts importDialogOptions) ([]string, error) {
	var filters []mygo.FileFilter
	if len(opts.Extensions) > 0 {
		filters = []mygo.FileFilter{{Name: locales.ImportFolderOrZip(), Extensions: opts.Extensions}}
	}
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      parent,
		Title:       opts.Title,
		ButtonLabel: opts.ButtonLabel,
		Directory:   opts.Directories && !opts.Files,
		Multiple:    opts.Multiple,
		Filters:     filters,
		Message:     opts.Message,
	})
}
