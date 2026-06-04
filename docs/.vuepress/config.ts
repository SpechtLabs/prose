import { viteBundler } from "@vuepress/bundler-vite";
import { registerComponentsPlugin } from "@vuepress/plugin-register-components";
import { path } from "@vuepress/utils";
import container from "markdown-it-container";
import { defineUserConfig } from "vuepress";
import { plumeTheme } from "vuepress-theme-plume";

export default defineUserConfig({
  base: "/",
  lang: "en-US",
  title: "prose",
  description:
    "Kubernetes operators that read like prose — a thin DSL over controller-runtime for linear, observable reconcilers",

  head: [
    [
      "meta",
      {
        name: "description",
        content:
          "prose is a thin DSL over controller-runtime for building Kubernetes operators as a linear, observable sequence of named steps. You describe what a reconcile does; prose handles the boilerplate and gives you tracing, wide-event logging, metrics, and Kubernetes events for free.",
      },
    ],
    ["link", { rel: "icon", type: "image/png", href: "/images/specht.png" }],
  ],

  bundler: viteBundler({
    viteOptions: {
      build: {
        // lightningcss (the default rolldown-vite CSS minifier) chokes under bun's
        // module layout; fall back to esbuild for CSS minification.
        cssMinify: "esbuild",
      },
    },
  }),
  shouldPrefetch: false,

  extendsMarkdown: (md) => {
    md.use(container, "terminal", {
      validate: (params: string) => {
        const info = params.trim();
        return /^terminal(?:\s+.*)?$/.test(info);
      },
      render: (tokens: any[], idx: number) => {
        const token = tokens[idx];
        if (token.nesting === 1) {
          const info = token.info.trim();
          const rest = info.replace(/^terminal\s*/, "");
          const attrs: Record<string, string> = {};
          const attrRegex = /(\w+)=((?:\"[^\"]*\")|(?:'[^']*')|(?:[^\s]+))/g;
          let consumed = "";
          let m: RegExpExecArray | null;
          while ((m = attrRegex.exec(rest)) !== null) {
            const key = m[1];
            let val = m[2];
            if ((val.startsWith('"') && val.endsWith('"')) || (val.startsWith("'") && val.endsWith("'"))) {
              val = val.slice(1, -1);
            }
            attrs[key] = val;
            consumed += m[0] + " ";
          }
          const positional = rest.replace(consumed, "").trim();
          const titleRaw = attrs.title ?? positional ?? "";
          const title = titleRaw ? md.utils.escapeHtml(titleRaw) : "";
          const titleAttr = title ? ` title=\"${title}\"` : "";
          return `\n<Terminal${titleAttr}>\n`;
        }
        return `\n</Terminal>\n`;
      },
    });
  },

  plugins: [
    registerComponentsPlugin({
      componentsDir: path.resolve(__dirname, "./components"),
    }),
  ],

  theme: plumeTheme({
    docsRepo: "https://github.com/spechtlabs/prose",
    docsDir: "docs",
    docsBranch: "main",

    editLink: true,
    lastUpdated: false,
    contributors: false,

    article: "/article/",

    cache: "filesystem",
    search: { provider: "local" },

    sidebar: {
      // Getting Started — the tutorial path, from zero to a working reconciler.
      "/getting-started/": [
        {
          text: "Getting Started",
          icon: "mdi:rocket-launch",
          prefix: "/getting-started/",
          items: [
            { text: "Overview", link: "overview", icon: "mdi:eye" },
            { text: "Prerequisites", link: "prerequisites", icon: "mdi:check-circle" },
            { text: "Your First Reconciler", link: "quick", icon: "mdi:flash", badge: "5 min" },
            { text: "The Wormhole Walkthrough", link: "comprehensive", icon: "mdi:book-open-page-variant" },
            { text: "Troubleshooting & Next Steps", link: "troubleshooting", icon: "mdi:wrench" },
          ],
        },
      ],

      // How-to Guides — task-oriented recipes.
      "/guides/": [
        {
          text: "How-to Guides",
          icon: "mdi:compass",
          prefix: "/guides/",
          items: [
            { text: "Wire Up Observability", link: "observability", icon: "mdi:telescope" },
            { text: "Gate Steps with Predicates", link: "gates", icon: "mdi:gate" },
            { text: "Handle Finalizers & Deletion", link: "finalizers", icon: "mdi:delete-clock" },
            { text: "Clean Up Safely", link: "cleanup", icon: "mdi:broom" },
            { text: "Use the Escape Hatch", link: "escape-hatch", icon: "mdi:door-open" },
            { text: "Test a Reconciler", link: "testing", icon: "mdi:test-tube" },
          ],
        },
      ],

      // Understanding — the explanation path: why prose is shaped the way it is.
      "/understanding/": [
        {
          text: "Understanding prose",
          icon: "mdi:lightbulb",
          collapsed: false,
          prefix: "/understanding/",
          items: [
            { text: "The Mental Model", link: "mental-model", icon: "mdi:thought-bubble" },
            { text: "Design Principles", link: "design-principles", icon: "mdi:compass-rose" },
            { text: "Observability as a Boundary", link: "observability", icon: "mdi:vector-square" },
            { text: "Borrowing from Ginkgo", link: "ginkgo", icon: "mdi:dna" },
          ],
        },
      ],

      // Reference — the precise, look-it-up material; godoc is the source of truth
      // for signatures, this is the map.
      "/reference/": [
        {
          text: "Reference",
          icon: "mdi:book",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "API & godoc", link: "api", icon: "mdi:api" },
            { text: "The Vocabulary", link: "vocabulary", icon: "mdi:format-list-bulleted" },
            { text: "Outcomes", link: "outcomes", icon: "mdi:directions-fork" },
          ],
        },
      ],
    },

    /**
     * markdown
     * @see https://theme-plume.vuejs.press/config/markdown/
     */
    markdown: {
      collapse: true,
      timeline: true,
      plot: true,
      repl: {
        go: true, // ::: go-repl
        rust: true, // ::: rust-repl
      },
      mermaid: true, // 启用 mermaid
      image: {
        figure: true,
        lazyload: true,
        mark: true,
        size: true,
      },
    },

    watermark: false,
  }),
});
