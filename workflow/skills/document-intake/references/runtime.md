# Portable runtimes

- MarkItDown 0.1.6: fallback parser. Resolve `EVIDENCE_MARKITDOWN`, then `PATH`, then `%USERPROFILE%\.codex\tools\evidence-to-delivery\markitdown\.venv\Scripts\markitdown.exe`.
- Docling 2.118.0: primary local parser. Resolve `EVIDENCE_DOCLING_PYTHON`, then `%USERPROFILE%\.codex\tools\evidence-to-delivery\docling\.venv\Scripts\python.exe`. This environment also installs openpyxl, pypdf and pypdfium2 for the bundled read-only spreadsheet/PDF branches.
- Supported format reference: https://docling-project.github.io/docling/usage/supported_formats/ ; the installed version must still be checked before claiming support for a particular file.
- Output must differ from source. The intake helper compares source SHA-256 before and after.
