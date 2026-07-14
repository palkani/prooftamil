import fs from "node:fs";
import path from "node:path";

/**
 * Blog content (§17.2 screen 13).
 *
 * Markdown files on disk, read at BUILD time — no database, no CMS, no auth. The plan's
 * md+DB+RSS hybrid can come later; a blog whose posts live in git is version-controlled,
 * reviewable in a PR, and impossible to take down by losing a database.
 */
export interface Post {
  slug: string;
  title: string;
  titleEn: string;
  date: string;
  excerpt: string;
  body: string;
}

const DIR = path.join(process.cwd(), "..", "..", "content", "blog");

/** A deliberately small front-matter parser — one dependency avoided. */
function parse(raw: string, slug: string): Post {
  const m = raw.match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/);
  const meta: Record<string, string> = {};
  let body = raw;

  if (m) {
    for (const line of m[1].split("\n")) {
      const i = line.indexOf(":");
      if (i < 0) continue;
      meta[line.slice(0, i).trim()] = line
        .slice(i + 1)
        .trim()
        .replace(/^["']|["']$/g, "");
    }
    body = m[2];
  }

  return {
    slug,
    title: meta.title ?? slug,
    titleEn: meta.titleEn ?? "",
    date: meta.date ?? "",
    excerpt: meta.excerpt ?? "",
    body: body.trim(),
  };
}

export function allPosts(): Post[] {
  if (!fs.existsSync(DIR)) return [];
  return fs
    .readdirSync(DIR)
    .filter((f) => f.endsWith(".md"))
    .map((f) => parse(fs.readFileSync(path.join(DIR, f), "utf8"), f.replace(/\.md$/, "")))
    .sort((a, b) => b.date.localeCompare(a.date));
}

export function getPost(slug: string): Post | undefined {
  return allPosts().find((p) => p.slug === slug);
}

/**
 * Minimal markdown → HTML. Handles what the posts actually use (headings, bold, code,
 * paragraphs) rather than pulling in a full parser for two files.
 *
 * Input is our own git-tracked content, not user input — but it is still escaped first,
 * because "it's our content" is exactly the assumption that rots the day someone adds a
 * CMS.
 */
export function toHtml(md: string): string {
  const esc = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

  return esc(md)
    .split(/\n\n+/)
    .map((block) => {
      const b = block.trim();
      if (b.startsWith("## ")) return `<h2>${inline(b.slice(3))}</h2>`;
      if (b.startsWith("# ")) return `<h1>${inline(b.slice(2))}</h1>`;
      return `<p>${inline(b).replace(/\n/g, "<br/>")}</p>`;
    })
    .join("\n");
}

function inline(s: string): string {
  return s
    .replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>")
    .replace(/\*(.+?)\*/g, "<em>$1</em>")
    .replace(/`(.+?)`/g, "<code>$1</code>");
}
