import { defineNavbarConfig } from "vuepress-theme-plume";

export const navbar = defineNavbarConfig([
  { text: "Home", link: "/", icon: "mdi:home" },

  {
    text: "Getting Started",
    icon: "mdi:rocket-launch",
    items: [
      { text: "Overview", link: "/getting-started/overview", icon: "mdi:eye" },
      { text: "Prerequisites", link: "/getting-started/prerequisites", icon: "mdi:check-circle" },
      { text: "Your First Reconciler", link: "/getting-started/quick", icon: "mdi:flash" },
      { text: "The Wormhole Walkthrough", link: "/getting-started/comprehensive", icon: "mdi:book-open-page-variant" },
      { text: "Troubleshooting", link: "/getting-started/troubleshooting", icon: "mdi:wrench" },
    ],
  },

  {
    text: "Guides",
    icon: "mdi:compass",
    items: [
      { text: "Wire Up Observability", link: "/guides/observability", icon: "mdi:telescope" },
      { text: "Gate Steps with Predicates", link: "/guides/gates", icon: "mdi:gate" },
      { text: "Finalizers & Deletion", link: "/guides/finalizers", icon: "mdi:delete-clock" },
      { text: "Clean Up Safely", link: "/guides/cleanup", icon: "mdi:broom" },
      { text: "The Escape Hatch", link: "/guides/escape-hatch", icon: "mdi:door-open" },
      { text: "Test a Reconciler", link: "/guides/testing", icon: "mdi:test-tube" },
    ],
  },

  {
    text: "Understanding",
    icon: "mdi:lightbulb",
    items: [
      { text: "The Mental Model", link: "/understanding/mental-model", icon: "mdi:thought-bubble" },
      { text: "Design Principles", link: "/understanding/design-principles", icon: "mdi:compass-rose" },
      { text: "Observability as a Boundary", link: "/understanding/observability", icon: "mdi:vector-square" },
      { text: "Borrowing from Ginkgo", link: "/understanding/ginkgo", icon: "mdi:dna" },
    ],
  },

  {
    text: "Reference",
    icon: "mdi:book",
    items: [
      { text: "API & godoc", link: "/reference/api", icon: "mdi:api" },
      { text: "The Vocabulary", link: "/reference/vocabulary", icon: "mdi:format-list-bulleted" },
      { text: "Outcomes", link: "/reference/outcomes", icon: "mdi:directions-fork" },
    ],
  },

  {
    text: "More",
    icon: "mdi:dots-horizontal",
    items: [
      {
        text: "API Docs (pkg.go.dev)",
        link: "https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose",
        target: "_blank",
        rel: "noopener noreferrer",
        icon: "mdi:language-go",
      },
      {
        text: "Releases",
        link: "https://github.com/spechtlabs/prose/releases",
        target: "_blank",
        rel: "noopener noreferrer",
        icon: "mdi:download",
      },
      {
        text: "Report an Issue",
        link: "https://github.com/spechtlabs/prose/issues/new/choose",
        target: "_blank",
        rel: "noopener noreferrer",
        icon: "mdi:bug-outline",
      },
    ],
  },
]);
