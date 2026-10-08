FileServer 端到端测试目录

样本规则：
- 小样本 fixture 入库：体积小、能覆盖边界（中文名 / 特殊字符 / 深层目录 /
  小视频 / 图片 / 文档），例如 videos/sample.mp4、manyvideos/*.mp4（各约 10~40KB）。
- 大样本与脚本生成的临时文件不入库，被 .gitignore 排除：
  big.mp4（损坏大文件）、__hls_*.mp4 / __hls_*.mkv（hls_e2e_test.py 生成）等。
- 脚本会在运行时就地生成上述临时样本；不要把它们提交进仓库。

已入库的小样本即仓库事实，不再在 .gitignore 里假装排除。
