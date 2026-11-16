package main

import "skillmanager/locales"

type importDialogOptions struct {
	Title       string
	ButtonLabel string
	Message     string
	Multiple    bool
	Files       bool
	Directories bool
	Extensions  []string
	UTIs        []string
}

func defaultImportDialogOptions() importDialogOptions {
	return importDialogOptions{
		Title:       locales.ActionAddSkills(),
		ButtonLabel: locales.ImportRead(),
		Message:     locales.ImportChooseFolderMessage(),
		Multiple:    true,
		Files:       true,
		Directories: true,
		Extensions:  []string{"zip"},
		UTIs:        []string{"public.folder"},
	}
}
