//go:build windows

package library

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"

	"skillmanager/locales"
)

// 目录联接（junction）是一个带挂载点重解析数据的空目录。这里用
// FSCTL_SET_REPARSE_POINT 直接写入，不经过 cmd 的 mklink：经过 cmd 时，
// 路径里的 & ^ % ( ) 会被当成命令语法，技能名就能拼出任意命令。
// 数据布局见 REPARSE_DATA_BUFFER 的 MountPointReparseBuffer：
// https://learn.microsoft.com/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_reparse_data_buffer
const (
	ioReparseTagMountPoint   = 0xA0000003
	fsctlSetReparsePoint     = 0x000900A4
	maxReparseDataBufferSize = 16 * 1024
	reparseHeaderSize        = 8 // ReparseTag + ReparseDataLength + Reserved
	mountPointHeaderSize     = 8 // 替代名、显示名各一组 Offset + Length
)

// makeJunction 在 dst 建一个指向 target 的目录联接。target 必须是本机盘符下
// 的绝对路径（联接不能指向网络路径）；dst 不能已经存在。
func makeJunction(target, dst string) error {
	data, err := mountPointReparseData(target)
	if err == nil {
		err = os.Mkdir(dst, 0o755)
	}
	if err == nil {
		if err = setReparsePoint(dst, data); err != nil {
			os.Remove(dst)
		}
	}
	if err != nil {
		return &os.LinkError{Op: "junction", Old: target, New: dst, Err: err}
	}
	return nil
}

// setReparsePoint 把重解析数据写到空目录 dir 上。
func setReparsePoint(dir string, data []byte) error {
	name, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	var returned uint32
	return syscall.DeviceIoControl(h, fsctlSetReparsePoint, &data[0], uint32(len(data)), nil, 0, &returned, nil)
}

// mountPointReparseData 编出指向 target 的挂载点重解析数据。PathBuffer 里
// 先放替代名（NT 路径 \??\C:\…），再放显示名（C:\…），各自以 NUL 结尾；
// Length 字段不计 NUL。
func mountPointReparseData(target string) ([]byte, error) {
	if vol := filepath.VolumeName(target); len(vol) != 2 || vol[1] != ':' || !filepath.IsAbs(target) {
		return nil, errors.New(locales.LinkJunctionRequiresLocalAbsolutePath())
	}
	subst := utf16.Encode([]rune(`\??\` + target))
	print := utf16.Encode([]rune(target))
	substLen, printLen := 2*len(subst), 2*len(print)
	dataLen := mountPointHeaderSize + substLen + 2 + printLen + 2
	if reparseHeaderSize+dataLen > maxReparseDataBufferSize {
		return nil, errors.New(locales.LinkJunctionPathTooLong())
	}

	b := make([]byte, reparseHeaderSize+dataLen)
	le := binary.LittleEndian
	le.PutUint32(b[0:], ioReparseTagMountPoint)
	le.PutUint16(b[4:], uint16(dataLen))
	le.PutUint16(b[8:], 0) // SubstituteNameOffset
	le.PutUint16(b[10:], uint16(substLen))
	le.PutUint16(b[12:], uint16(substLen+2)) // PrintNameOffset：跳过替代名和它的 NUL
	le.PutUint16(b[14:], uint16(printLen))
	pathBuffer := b[reparseHeaderSize+mountPointHeaderSize:]
	for i, c := range subst {
		le.PutUint16(pathBuffer[2*i:], c)
	}
	for i, c := range print {
		le.PutUint16(pathBuffer[substLen+2+2*i:], c)
	}
	return b, nil
}
