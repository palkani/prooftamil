import Link from "next/link";

/** Production's "Related reading" strip — a pair of GUIDE cards under each tool page. */
export default function Related({
  items,
}: {
  items: { href: string; kicker?: string; title: string }[];
}) {
  return (
    <section className="pt-related">
      <div className="pt-related-inner">
        <h2>Related reading</h2>
        <div className="pt-related-grid">
          {items.map((it) => (
            <Link key={it.href + it.title} href={it.href} className="pt-related-card">
              <div className="kicker">{it.kicker ?? "GUIDE"}</div>
              <h3>{it.title}</h3>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}
