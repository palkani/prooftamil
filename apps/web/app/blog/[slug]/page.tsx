import { notFound } from "next/navigation";

import { allPosts, getPost, toHtml } from "@/lib/blog";

export function generateStaticParams() {
  return allPosts().map((p) => ({ slug: p.slug }));
}

export default async function Post({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const post = getPost(slug);
  if (!post) notFound();

  return (
    <main className="pt-landing">
      <header className="pt-header">
        <h1>
          <a href="/">ProofTamil</a>
        </h1>
      </header>

      <article className="pt-article">
        <h1 lang="ta">{post.title}</h1>
        <p className="pt-post-en">{post.titleEn}</p>
        <time>{post.date}</time>
        <div
          className="pt-article-body"
          lang="ta"
          dangerouslySetInnerHTML={{ __html: toHtml(post.body) }}
        />
      </article>

      <p className="pt-article-back">
        <a href="/blog">← All posts</a>
      </p>
    </main>
  );
}
