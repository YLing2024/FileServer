"""从 dist exe 中解出嵌入的 app.js，直接检查是否含 kindByExt 和 rememberScroll 修复"""
import sys
import re

exe_path = "dist/FileServer.exe"
data = open(exe_path, "rb").read()

find_ascii = data.find(b"kindByExt")
find_scroll = data.find(b"rememberScroll")
print(f"exe 中 kindByExt 偏移: {find_ascii}")
print(f"exe 中 rememberScroll 偏移: {find_scroll}")

if find_ascii == -1 or find_scroll == -1:
    print("!!! 异常: exe 中未找到修复符号，说明 exe 是旧代码 !!!")
    sys.exit(1)
print("exe 确实含两个修复符号 → exe 是最新代码")
