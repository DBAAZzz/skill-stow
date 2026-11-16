//go:build darwin

package main

import (
	"slices"
	"testing"
)

func TestDarwinImportDialogResolvesZipAndFolderUTTypes(t *testing.T) {
	withObjCPool(func() {
		arr, ids := resolveImportContentTypes(defaultImportDialogOptions())
		if arr == 0 {
			t.Fatal("应返回有效的 UTType NSArray")
		}
		if !slices.Equal(ids, []string{"public.zip-archive", "public.folder"}) {
			t.Fatalf("UTType 列表 = %v, want [public.zip-archive public.folder]", ids)
		}
	})
}
