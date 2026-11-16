@echo off
rem Skill 管理器 · Windows 本地开发启动
rem   dev.cmd        用示例数据启动（不碰本机 Agent 目录）
rem   dev.cmd real   读本机真实数据
setlocal
cd /d "%~dp0"

if /i "%~1"=="real" goto real

set "SKILLS_SAMPLE=1"
echo [示例数据] 只读示例，不会读写本机 Agent 目录
goto run

:real
echo [本机数据] 启动后「添加到 Agent」会链接到你的真实目录

:run
go tool mygo dev
