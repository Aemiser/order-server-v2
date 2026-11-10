/**
 * CHANGELOG.md 片段的极简 Markdown 渲染器。
 *
 * 设计取舍：更新日志来自编译进 exe 的仓库内 CHANGELOG.md，格式固定
 * （### 分组 / - 列表 / > 说明 / 行内代码），无需引入 marked、DOMPurify 等依赖，
 * 桌面端离线可用、体积为零。
 *
 * 安全性：先对整行做 HTML 转义，只在转义后的文本上插入**我们自己写死**的标签，
 * 用户输入永远不会变成属性或脚本；不解析链接，避免 javascript: 之类的注入面。
 */

const escapeHtml = (text: string): string =>
  text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');

/** 行内格式：行内代码 + 粗体。代码内容用占位符保护，避免其中的 ** 被当成粗体。 */
function renderInline(text: string): string {
  const codes: string[] = [];
  let out = escapeHtml(text).replace(/`([^`]+)`/g, (_m, code: string) => {
    codes.push(`<code>${code}</code>`);
    return `\u0000${codes.length - 1}\u0000`;
  });
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  return out.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => codes[Number(i)] ?? '');
}

/**
 * 把一段 Markdown 渲染成 HTML，交给 v-html 显示。
 * 支持：# ~ ###### 标题、- / * / + 与 1. 列表、> 引用、--- 分隔线、段落、
 * 行内 `代码` 与 **粗体**。所有标签都有 index.css 里 .changelog-md 下的对应样式。
 */
export function renderChangelogMarkdown(md: string): string {
  const lines = (md || '').replace(/\r\n/g, '\n').split('\n');
  const blocks: string[] = [];
  let listItems: string[] = [];
  let listOrdered = false;
  let quoteLines: string[] = [];
  let paraLines: string[] = [];

  const flush = () => {
    if (listItems.length) {
      const tag = listOrdered ? 'ol' : 'ul';
      blocks.push(`<${tag}>${listItems.map((i) => `<li>${i}</li>`).join('')}</${tag}>`);
      listItems = [];
    }
    if (quoteLines.length) {
      blocks.push(`<blockquote>${quoteLines.map((i) => `<p>${i}</p>`).join('')}</blockquote>`);
      quoteLines = [];
    }
    if (paraLines.length) {
      blocks.push(`<p>${paraLines.join('<br>')}</p>`);
      paraLines = [];
    }
  };

  for (const raw of lines) {
    const t = raw.trim();
    if (t === '') {
      flush();
      continue;
    }

    if (/^-{3,}$/.test(t)) {
      flush();
      blocks.push('<hr>');
      continue;
    }

    const heading = /^(#{1,6})\s+(.+)$/.exec(t);
    if (heading) {
      flush();
      const level = heading[1].length;
      blocks.push(`<h${level}>${renderInline(heading[2])}</h${level}>`);
      continue;
    }

    const quote = /^>\s?(.*)$/.exec(t);
    if (quote) {
      if (listItems.length || paraLines.length) flush();
      quoteLines.push(renderInline(quote[1]));
      continue;
    }

    const bullet = /^[-*+]\s+(.+)$/.exec(t);
    const numbered = /^\d+[.)]\s+(.+)$/.exec(t);
    if (bullet || numbered) {
      if (quoteLines.length || paraLines.length || (listItems.length && listOrdered !== !!numbered)) flush();
      listOrdered = !!numbered;
      listItems.push(renderInline((bullet || numbered)![1]));
      continue;
    }

    if (listItems.length || quoteLines.length) flush();
    paraLines.push(renderInline(t));
  }
  flush();
  return blocks.join('');
}
