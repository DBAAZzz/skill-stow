package main

import (
	"slices"
	"testing"
)

func TestImportDialogOptionsAllowFoldersAndZipsSimultaneously(t *testing.T) {
	opts := defaultImportDialogOptions()
	if !opts.Files || !opts.Directories || !opts.Multiple {
		t.Fatalf("应同时允许选取文件和文件夹且支持多选：%+v", opts)
	}
	if !slices.Equal(opts.Extensions, []string{"zip"}) {
		t.Errorf("Extensions = %v, want [zip]", opts.Extensions)
	}
	if !slices.Equal(opts.UTIs, []string{"public.folder"}) {
		t.Errorf("UTIs = %v, want [public.folder]", opts.UTIs)
	}
	if opts.Title == "" || opts.ButtonLabel == "" || opts.Message == "" {
		t.Errorf("对话框标题/按钮/说明不能为空：%+v", opts)
	}
}
