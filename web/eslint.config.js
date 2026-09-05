import js from "@eslint/js"
import tseslint from "typescript-eslint"
import reactHooks from "eslint-plugin-react-hooks"

export default tseslint.config(
  { ignores: ["dist/**", "node_modules/**"] },
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // Report, do not gate -- the same trade the npm audit step makes.
      //
      // 7.0 folded the React Compiler's diagnostics into `recommended`, and
      // these two land on fourteen sites that were written deliberately and
      // carry a comment saying why: the Back-button mode sync in
      // playground-screen, the throttle in markdown, the id counters read by
      // a useState initialiser in routing. Each needs its own rewrite and its
      // own reasoning about what the effect was holding together, which is
      // not something a dependency bump should be doing to a live console.
      //
      // They stay visible as warnings so the count cannot quietly grow while
      // the fourteen are worked through.
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/refs": "warn",
      // A leading underscore is the codebase's mark for a binding that exists
      // only to be discarded -- the two columns `toStoredConfig` strips, the
      // chunk `drain` throws away. Flagging those is flagging the convention.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" },
      ],
      // A feature is reached through its index or not at all. Reaching into
      // another feature's files is how one screen's refactor breaks a second
      // screen nobody thought was involved.
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@/features/*/*", "!@/features/*/index"],
              message:
                "Import another feature through its index (@/features/<name>), not from a file inside it.",
            },
          ],
        },
      ],
    },
  },
)
