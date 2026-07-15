package executil

// PowerShellUTF8EncodingPrefix is prepended to every Windows PowerShell command
// so that child-process output is interpreted and forwarded as UTF-8.  Without
// this, a Chinese Windows host (default codepage 936 / GBK) decodes UTF-8 bytes
// from Python or other tools as GBK, producing the diamond-question-mark
// garbling users see in terminal output.
const PowerShellUTF8EncodingPrefix = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $OutputEncoding = [System.Text.Encoding]::UTF8; $env:PYTHONIOENCODING='utf-8'; `
