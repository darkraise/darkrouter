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
