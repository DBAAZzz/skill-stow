package library

import (
	"testing"

	"github.com/egoist/mygo/i18n"
)

func TestAppDataStaysInOneFolderWhateverTheInterfaceLanguage(t *testing.T) {
	// Given 同一个 home
	home := t.TempDir()
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })

	// When 界面分别是中文和英文
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	zh := appDir(home)
	i18n.SetLocaleFunc(func() string { return "en" })
	en := appDir(home)

	// Then 设置、停用记录都在同一个目录
	if zh != en {
		t.Errorf("换界面语言后数据目录变了：%s → %s", zh, en)
	}
}
