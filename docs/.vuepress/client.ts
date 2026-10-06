import { defineClientConfig } from "vuepress/client";

export default defineClientConfig({
  enhance() {
    // The shared components (Terminal, ListCompare, the Contributors and
    // Releases home sections, ...) come from @spechtlabs/docs-kit, which
    // registers them itself (see config.ts).
  },
});
