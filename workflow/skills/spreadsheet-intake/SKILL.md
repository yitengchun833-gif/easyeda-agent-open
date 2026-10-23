---
name: spreadsheet-intake
description: Read local spreadsheet values, formulas and source locations for the unified document intake workflow; preserve the original workbook.
---

# 表格解析分支

由 document-intake 按需调用。仅负责读取，不修改工作簿，不执行宏或刷新外部链接。

使用新电脑上可用的表格 Skill 进行只读分析；缺少该 Skill 时，使用安装脚本提供的 Docling Python 环境和 openpyxl：分别以 `read_only=True, data_only=False` 和 `read_only=True, data_only=True` 打开 XLSX/XLSM，比较同一单元格公式与缓存值。仅遍历目标工作表和必要范围；结束关闭工作簿，不调用 save。

先列工作表，识别单位、表头、隐藏工作表及跨表引用，再读取目标范围。缺失缓存不等于零；openpyxl 不计算公式，需最新计算值时记录缺口。旧 XLS、ODS 等格式使用实际支持的读取器，不能换扩展名冒充 XLSX。CSV 使用标准库按明确编码与分隔符读取。

输出工作表名、单元格范围、原始值/公式、单位、必要假设与缺口到项目 findings.md。图表和嵌入图片需要实际查看；结构读取不等于图面验收。不得将源表或本机账号路径复制进便携工作流包。
