# Contributing

Thank you for improving `vllm-use`. Keep Phase 1 changes small, auditable, native, and honest about upstream vLLM behavior.

1. Open an issue for substantial behavior or security changes.
2. Create a focused branch and do not include generated editor files, credentials, model data, or unrelated formatting.
3. Add tests with behavior changes. Prefer standard-library Go tests and Vitest/React Testing Library for UI behavior.
4. Run:

   ```bash
   gofmt -w <changed-go-files>
   go test ./...
   go vet ./...
   cd web
   bun install
   bun run lint
   bun run typecheck
   bun run test
   bun run build
   ```

5. Verify `go build ./cmd/vllm-use` after `web/dist` is rebuilt—the dist is part of the binary and should accompany intentional UI source changes.

Use Bun as the frontend package manager; do not add npm, Yarn, or pnpm lockfiles or commands. TypeScript stays strict (`noUncheckedIndexedAccess` and `exactOptionalPropertyTypes` included); avoid broad `any`, suppressions, large UI kits, and unnecessary client state frameworks. Keep API types centralized in `web/src/api.ts`.

Document new flags, endpoints, scopes, persistence, and limitations in both READMEs where user-visible. Never weaken path containment, bearer authentication, origin validation, constant-time secret comparison, or destructive confirmations without a security review.

By contributing, you agree that your work is provided under the repository's BSD 3-Clause License.
