import { allPosts } from "@/lib/blog";

export const metadata = { title: "Blog — ProofTamil" };

export default function Blog() {
  const posts = allPosts();
  return (
    <main className="pt-landing">
      <header className="pt-header">
        <h1>
          <a href="/">ProofTamil</a> · Blog
        </h1>
        <p>On Tamil, and on building software that reads it.</p>
      </header>

      {posts.map((p) => (
        <a key={p.slug} className="pt-post-card" href={`/blog/${p.slug}`}>
          <h2 lang="ta">{p.title}</h2>
          <p className="pt-post-en">{p.titleEn}</p>
          <p className="pt-post-ex">{p.excerpt}</p>
          <time>{p.date}</time>
        </a>
      ))}

      <footer className="pt-foot">
        <a href="/">Home</a>
        <a href="/write">Editor</a>
      </footer>
    </main>
  );
}
