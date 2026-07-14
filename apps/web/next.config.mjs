/** @type {import('next').NextConfig} */
export default {
  reactStrictMode: true,

  /**
   * The production build writes to .next-build, NOT .next.
   *
   * `next dev` and `next build` both default to .next, so running a build while the dev
   * server is up lets the build overwrite the chunks the dev server is actively serving.
   * The dev server then 500s with "Cannot find module './969.js'" — a real error with a
   * completely misleading message, since nothing is wrong with the code.
   *
   * Separating the directories makes the collision impossible rather than merely unlikely.
   */
  distDir: process.env.NODE_ENV === "production" ? ".next-build" : ".next",
};
