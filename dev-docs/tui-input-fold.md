# TUI Input Fold — 大量文本粘贴折叠显示

> 当用户在 TUI 输入框中粘贴大量文本时，可以折叠显示为 `[N lines pasted]` 提示，按 Tab 展开/折叠。

## 1. 目标

在 TUI REPL 中粘贴大段文本（如错误日志、代码片段）时，输入框会占据大量终端空间。折叠功能让输入框在粘贴多行文本时自动收缩为单行提示，保持界面简洁，同时保留完整内容供提交。

## 2. 触发条件

- **折叠阈值**: ≥5 行文本
- **触发方式**: 按 `Tab` 键手动折叠/展开
- **显示**: 折叠时显示 `[N lines pasted · Tab to expand]`

## 3. 实现细节

### 3.1 数据结构

在 `tuiModel` 中添加三个字段：

```go
// inputFolded is set when the input box contains a large amount of pasted
// text. When true, the textarea is collapsed and shows a summary like
// "[123 lines pasted]" instead of the full content. Tab toggles.
inputFolded bool

// inputFoldedLines stores the line count when folded for display.
inputFoldedLines int

// foldedFullText holds the complete input text when inputFolded is true.
// Cleared when expanded.
foldedFullText string
```

### 3.2 折叠逻辑

在 `handleKey` 函数中处理 Tab 键：

```go
// Folded state toggle: Tab expands/collapses when there's multi-line content.
// This check runs before the image-path detection so it works even when
// folded. Tab is only captured when the completion menu is not open.
if msg.Type == tea.KeyTab && len(m.complItems) == 0 {
    if m.inputFolded {
        // Expand: restore the full text
        m.ta.SetValue(m.foldedFullText)
        m.inputFolded = false
        m.foldedFullText = ""
        return m, m.updateTextAreaHeight()
    }
    // Collapse: fold if there are many lines
    if lines := strings.Count(m.ta.Value(), "\n") + 1; lines >= 5 {
        m.foldedFullText = m.ta.Value()
        m.inputFolded = true
        m.inputFoldedLines = lines
        return m, nil
    }
}
```

### 3.3 渲染逻辑

在 `renderInputBox` 中根据折叠状态显示不同内容：

```go
func (m *tuiModel) renderInputBox() string {
    if m.inputFolded {
        // When folded, show a compact placeholder instead of the full textarea.
        // The textarea still exists (holds cursor, etc.) but is hidden.
        label := fmt.Sprintf("[ %d lines pasted · Tab to expand ]", m.inputFoldedLines)
        return hintStyle.Render(label)
    }
    return m.ta.View()
}
```

### 3.4 提交时使用完整文本

在 `submit` 和 `Ctrl+Q` 处理中，如果处于折叠状态，使用存储的完整文本：

```go
func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
    // If folded, expand to get the full text for submission.
    text := m.ta.Value()
    if m.inputFolded {
        text = m.foldedFullText
    }
    text = strings.TrimSpace(text)
    // ... rest of submit logic
}
```

### 3.5 状态清理

折叠状态在以下情况下会被清理：
- **提交后** (`submit`): 重置 `inputFolded`, `foldedFullText`, `inputFoldedLines`
- **Esc 清空输入时**: 重置折叠状态
- **Ctrl+Q 排队后**: 重置折叠状态

## 4. 用户体验

### 4.1 典型工作流

1. 用户从别处复制一段 20 行的错误日志
2. 粘贴到 TUI 输入框（Cmd+V / Ctrl+V）
3. 按 `Tab` 折叠，输入框显示 `[20 lines pasted · Tab to expand]`
4. 可以按 `Tab` 展开查看/编辑内容
5. 按 `Enter` 提交，完整文本发送给 agent

### 4.2 快捷键

| 快捷键 | 行为 |
|--------|------|
| `Tab` | 折叠/展开多行输入（≥5 行时） |
| `Enter` | 提交（使用完整文本） |
| `Esc` | 清空输入并清除折叠状态 |
| `Ctrl+Q` | 排队（使用完整文本） |

### 4.3 边界情况

- **< 5 行**: Tab 键不触发折叠，保持正常输入
- **Completion 菜单打开时**: Tab 优先用于补全，不触发折叠
- **折叠时粘贴图片**: 图片粘贴不受影响，折叠状态保持
- **折叠时输入新文本**: 折叠状态先展开，新文本追加

## 5. 测试

测试覆盖以下场景：

- `TestTUI_InputFold_TabToggles`: Tab 折叠/展开切换
- `TestTUI_InputFold_SubmitUsesFullText`: 提交使用完整文本
- `TestTUI_InputFold_EscClears`: Esc 清除折叠状态
- `TestTUI_InputFold_CtrlQUsesFullText`: Ctrl+Q 使用完整文本
- `TestTUI_InputFold_SingleLineNoFold`: 单行不折叠
- `TestTUI_InputFold_FourLinesNoFold`: 4 行不折叠（阈值是 5）

## 6. 未来改进

- [ ] 自动折叠：粘贴超过 N 行时自动折叠（无需按 Tab）
- [ ] 可配置阈值：通过 config 调整折叠触发的行数
- [ ] 折叠预览：显示折叠文本的前几行作为预览
- [ ] 语法高亮：展开时根据内容类型显示语法高亮提示

## 7. 相关文件

- `cmd/octo/tuirepl.go`: 添加 `inputFolded`, `inputFoldedLines`, `foldedFullText` 字段
- `cmd/octo/tuirepl_view.go`: 实现折叠逻辑（`handleKey`, `renderInputBox`, `submit`）
- `cmd/octo/tuirepl_test.go`: 添加折叠功能测试用例
