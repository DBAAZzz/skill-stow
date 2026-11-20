## 中文

{{.ZH}}

### 安装

下载 `{{.DMGName}}`（通用版，Apple Silicon 和 Intel Mac 都能用，需要 macOS 12 或更新版本），把 Skill Stow 拖进「应用程序」，替换旧版即可，设置和记录都会保留。已安装 0.2.0 或更新版本时，也可以直接在应用里选择「Skill Stow › 检查应用更新…」完成更新。

这个版本还没有用 Apple 开发者证书签名，手动安装后要再放行一次：

- **macOS 15 及以后**：先双击打开一次，在提示里点「完成」；然后打开「系统设置 › 隐私与安全性」，滚到「安全性」，点 Skill Stow 旁边的「仍要打开」，输入密码确认。
- **macOS 12 到 14**：在「应用程序」里按住 Control 点 Skill Stow，选「打开」，再点「打开」。

也可以在终端里执行：

```sh
xattr -dr com.apple.quarantine "/Applications/Skill Stow.app"
```

---

## English

{{.EN}}

### Install

Download `{{.DMGName}}` (universal build for Apple Silicon and Intel Macs, macOS 12 or later) and drag Skill Stow into Applications to replace the old version. Your settings and records are kept. If you already have 0.2.0 or later, you can also update inside the app via Skill Stow › Check for App Updates….

This build isn't signed with an Apple Developer ID yet, so a version you install by hand needs to be allowed once:

- **macOS 15 and later**: open the app once and click Done. Then go to System Settings › Privacy & Security, scroll to Security, click **Open Anyway** next to Skill Stow and confirm with your password.
- **macOS 12 to 14**: in Applications, Control-click Skill Stow, choose Open, then click Open.

Or run this in Terminal:

```sh
xattr -dr com.apple.quarantine "/Applications/Skill Stow.app"
```

---

SHA-256 (`{{.DMGName}}`): `{{.DMGSHA256}}`
