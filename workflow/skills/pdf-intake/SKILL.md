---
name: pdf-intake
description: Render and inspect decision-critical PDF pages and diagrams to verify document extraction, without altering the original.
---

# PDF 与图片核对分支

由 document-intake 按需调用。使用本机 PDF Skill 的只读路径或现有页面查看工具；无 Poppler 时使用安装环境提供的 pypdfium2 渲染指定页：

```python
import pypdfium2 as pdfium
doc = pdfium.PdfDocument(source_path)
page = doc[page_number - 1]
bitmap = page.render(scale=2)
bitmap.to_pil().save(output_png)
bitmap.close()
page.close()
doc.close()
```

source_path 与 output_png 必须不同，派生图保存到项目资料目录。用实际图片查看工具打开输出，逐项核对影响结论的表头、单位、正负号、上下标、公式、箭头、连接关系与脚注。扫描件先 OCR 再看原图；独立图片直接查看。文本提取用 Docling，必要时 pypdf 补查；两份文本一致不能替代视觉检查。

记录原文件、页码（注明 PDF 页序与印刷页码差异）、核对项及未辨明内容。查看工具不可用时明确标为未验证，不凭文本推定电路图或图表含义。禁用远端 OCR/视觉 API，除非用户另行授权上传该资料。
