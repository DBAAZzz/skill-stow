//go:build darwin

package main

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

const (
	nsModalResponseOK    = 1
	nsUTF8StringEncoding = 4
)

var (
	darwinDialogOnce sync.Once
	darwinMsgSend    uintptr
	darwinPoolPush   uintptr
	darwinPoolPop    uintptr

	darwinObjCMu  sync.RWMutex
	darwinSelMap  = map[string]objc.SEL{}
	darwinClsMap  = map[string]objc.ID{}
)

func ensureDarwinDialogLoaded() {
	darwinDialogOnce.Do(func() {
		loadLib := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			if err != nil {
				panic(fmt.Sprintf("skillmanager: cannot load %s: %v", path, err))
			}
			return h
		}
		libObjC := loadLib("/usr/lib/libobjc.A.dylib")
		loadLib("/System/Library/Frameworks/Foundation.framework/Foundation")
		loadLib("/System/Library/Frameworks/AppKit.framework/AppKit")
		_, _ = purego.Dlopen("/System/Library/Frameworks/UniformTypeIdentifiers.framework/UniformTypeIdentifiers", purego.RTLD_GLOBAL|purego.RTLD_NOW)

		var err error
		if darwinMsgSend, err = purego.Dlsym(libObjC, "objc_msgSend"); err != nil {
			panic(err)
		}
		if darwinPoolPush, err = purego.Dlsym(libObjC, "objc_autoreleasePoolPush"); err != nil {
			panic(err)
		}
		if darwinPoolPop, err = purego.Dlsym(libObjC, "objc_autoreleasePoolPop"); err != nil {
			panic(err)
		}
	})
}

func withObjCPool(fn func()) {
	ensureDarwinDialogLoaded()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(darwinPoolPush)
	defer purego.SyscallN(darwinPoolPop, pool)
	fn()
}

func objcSel(name string) objc.SEL {
	darwinObjCMu.RLock()
	s, ok := darwinSelMap[name]
	darwinObjCMu.RUnlock()
	if ok {
		return s
	}
	s = objc.RegisterName(name)
	darwinObjCMu.Lock()
	darwinSelMap[name] = s
	darwinObjCMu.Unlock()
	return s
}

func objcClass(name string) objc.ID {
	darwinObjCMu.RLock()
	c, ok := darwinClsMap[name]
	darwinObjCMu.RUnlock()
	if ok {
		return c
	}
	c = objc.ID(objc.GetClass(name))
	if c == 0 {
		panic("skillmanager: Objective-C class " + name + " not found")
	}
	darwinObjCMu.Lock()
	darwinClsMap[name] = c
	darwinObjCMu.Unlock()
	return c
}

//go:uintptrescapes
func objcSend(obj objc.ID, selector string, args ...uintptr) objc.ID {
	var a [8]uintptr
	a[0], a[1] = uintptr(obj), uintptr(objcSel(selector))
	n := copy(a[2:], args)
	r, _, _ := purego.SyscallN(darwinMsgSend, a[:n+2]...)
	return objc.ID(r)
}

//go:uintptrescapes
func objcSendBool(obj objc.ID, selector string, args ...uintptr) bool {
	return byte(objcSend(obj, selector, args...)) != 0
}

func objcBoolArg(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}

func objcNSString(s string) objc.ID {
	var ptr uintptr
	if len(s) > 0 {
		ptr = uintptr(unsafe.Pointer(unsafe.StringData(s)))
	}
	str := objcSend(objcSend(objcClass("NSString"), "alloc"), "initWithBytes:length:encoding:",
		ptr, uintptr(len(s)), nsUTF8StringEncoding)
	runtime.KeepAlive(s)
	return objcSend(str, "autorelease")
}

func objcGoString(str objc.ID) string {
	if str == 0 {
		return ""
	}
	p := uintptr(objcSend(str, "UTF8String"))
	if p == 0 {
		return ""
	}
	n := int(objcSend(str, "lengthOfBytesUsingEncoding:", nsUTF8StringEncoding))
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n))
}

func objcNSArray(objs ...objc.ID) objc.ID {
	arr := objcSend(objcClass("NSMutableArray"), "arrayWithCapacity:", uintptr(len(objs)))
	for _, o := range objs {
		objcSend(arr, "addObject:", uintptr(o))
	}
	return arr
}

// resolveImportContentTypes 把扩展名和 UTI 转成 macOS 11+ 的 UTType 数组，
// 让开放面板同时允许选中文件夹（public.folder）和 .zip 压缩包。
func resolveImportContentTypes(opts importDialogOptions) (objc.ID, []string) {
	ensureDarwinDialogLoaded()
	if objc.GetClass("UTType") == 0 {
		return 0, nil
	}
	utTypeCls := objcClass("UTType")
	var types []objc.ID
	var ids []string
	for _, ext := range opts.Extensions {
		ext = strings.TrimPrefix(ext, ".")
		if ext == "" {
			continue
		}
		if t := objcSend(utTypeCls, "typeWithFilenameExtension:", uintptr(objcNSString(ext))); t != 0 {
			types = append(types, t)
			ids = append(ids, objcGoString(objcSend(t, "identifier")))
		}
	}
	for _, uti := range opts.UTIs {
		if uti == "" {
			continue
		}
		if t := objcSend(utTypeCls, "typeWithIdentifier:", uintptr(objcNSString(uti))); t != 0 {
			types = append(types, t)
			ids = append(ids, objcGoString(objcSend(t, "identifier")))
		}
	}
	if len(types) == 0 {
		return 0, nil
	}
	return objcNSArray(types...), ids
}

func applyImportPanelOptions(panel objc.ID, opts importDialogOptions) {
	if opts.Title != "" {
		objcSend(panel, "setTitle:", uintptr(objcNSString(opts.Title)))
	}
	if opts.Message != "" {
		objcSend(panel, "setMessage:", uintptr(objcNSString(opts.Message)))
	}
	if opts.ButtonLabel != "" {
		objcSend(panel, "setPrompt:", uintptr(objcNSString(opts.ButtonLabel)))
	}
	objcSend(panel, "setAllowsMultipleSelection:", objcBoolArg(opts.Multiple))
	objcSend(panel, "setTreatsFilePackagesAsDirectories:", 0)

	if arr, _ := resolveImportContentTypes(opts); arr != 0 {
		objcSend(panel, "setAllowedContentTypes:", uintptr(arr))
	} else {
		var names []objc.ID
		for _, ext := range opts.Extensions {
			if ext = strings.TrimPrefix(ext, "."); ext != "" {
				names = append(names, objcNSString(ext))
			}
		}
		for _, uti := range opts.UTIs {
			if uti != "" {
				names = append(names, objcNSString(uti))
			}
		}
		if len(names) > 0 {
			objcSend(panel, "setAllowedFileTypes:", uintptr(objcNSArray(names...)))
		}
	}
	// 在 allowedContentTypes 之后再明确设一次，避免系统按文件类型把目录选框关掉
	objcSend(panel, "setCanChooseFiles:", objcBoolArg(opts.Files))
	objcSend(panel, "setCanChooseDirectories:", objcBoolArg(opts.Directories))
}

func openPanelPaths(panel objc.ID) []string {
	urls := objcSend(panel, "URLs")
	if urls == 0 {
		return nil
	}
	n := int(objcSend(urls, "count"))
	paths := make([]string, 0, n)
	for i := range n {
		u := objcSend(urls, "objectAtIndex:", uintptr(i))
		if p := objcGoString(objcSend(u, "path")); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// openImportDialog 在 macOS 上直接打开允许同时选取文件夹和 .zip 的 NSOpenPanel。
// mygo.Dialog.Open 的 Directory 开关把文件/文件夹做成了互斥项，只能借原生面板绕开。
func openImportDialog(parent *mygo.Window, opts importDialogOptions) ([]string, error) {
	var parentHandle uintptr
	if parent != nil && !parent.IsDestroyed() {
		parentHandle = parent.NativeHandle()
	}
	ch := make(chan []string, 1)
	ran := false
	mygo.RunOnMain(func() {
		ran = true
		withObjCPool(func() {
			panel := objcSend(objcClass("NSOpenPanel"), "openPanel")
			applyImportPanelOptions(panel, opts)
			if parentHandle != 0 && objcSendBool(objc.ID(parentHandle), "isVisible") {
				objcSend(panel, "retain")
				blk := objc.NewBlock(func(_ objc.Block, resp int) {
					withObjCPool(func() {
						var paths []string
						if resp == nsModalResponseOK {
							paths = openPanelPaths(panel)
						}
						objcSend(panel, "release")
						ch <- paths
					})
				})
				objcSend(panel, "beginSheetModalForWindow:completionHandler:", parentHandle, uintptr(blk))
				blk.Release()
				return
			}
			nsApp := objcSend(objcClass("NSApplication"), "sharedApplication")
			objcSend(nsApp, "activateIgnoringOtherApps:", 1)
			var paths []string
			if int(objcSend(panel, "runModal")) == nsModalResponseOK {
				paths = openPanelPaths(panel)
			}
			ch <- paths
		})
	})
	if !ran {
		return nil, nil
	}
	return <-ch, nil
}
