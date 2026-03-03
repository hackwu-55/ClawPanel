# 1.Windows版本的安装脚本 powershell 版本问题

这个错误的核心原因是：你当前使用的是 Windows 自带的 Windows PowerShell 5.1，而 install.ps1 脚本中使用了 PowerShell 7.0 及以上版本才支持的三元运算符（? :），导致语法解析失败。
🔍 问题定位
报错行：[System.Environment]::Is64BitOperatingSystem ? 'x64' : 'x86'
原因：Windows PowerShell 5.1 不支持三元运算符，只有 PowerShell 7.0+ 才支持。
你的终端标题显示为 “Windows PowerShell”，这正是 5.1 版本，而非跨平台的 PowerShell 7.x。
✅ 解决方案（推荐）
方案一：升级到 PowerShell 7.x（推荐）
以管理员身份打开终端，执行以下命令安装最新版 PowerShell：
powershell
winget install --id Microsoft.PowerShell
安装完成后，从开始菜单打开 “PowerShell 7”（终端标题会显示 “PowerShell 7”）。
在新的 PowerShell 7 终端中重新执行安装命令：
powershell
irm https://raw.githubusercontent.com/zhaoxinyi02/ClawPanel/main/scripts/install.ps1 | iex
方案二：修改脚本以兼容旧版本
先下载脚本到本地：
powershell
irm https://raw.githubusercontent.com/zhaoxinyi02/ClawPanel/main/scripts/install.ps1 -OutFile install.ps1
用记事本或 VS Code 打开 install.ps1，找到第 53 行的三元运算符代码：
powershell
$arch = [System.Environment]::Is64BitOperatingSystem ? 'x64' : 'x86'
将其替换为兼容 5.1 的 if-else 写法：
powershell
if ([System.Environment]::Is64BitOperatingSystem) {
    $arch = 'x64'
} else {
    $arch = 'x86'
}
保存文件后，在管理员终端中执行：
powershell
.\install.ps1
💡 补充说明
检查当前 PowerShell 版本：执行 $PSVersionTable.PSVersion，如果主版本号小于 7，就需要升级。
升级到 PowerShell 7.x 后，不仅能解决这个问题，还能获得更好的性能和更多新特性。

# 2.安装脚本sh和ps1版本是写死的，应该自动获取release的最新版本，然后进行下载安装
# 3.Windows用户一键安装openclaw仍然提示没有nodejs，直接退出，返回code 1