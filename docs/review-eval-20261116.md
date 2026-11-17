# Permission-review evaluation, 2026-11-16

`ggrun-client review-eval` sends 40 fixed cases (`cmd/ggrun-client/testdata/review-cases.json`:
20 that must be allowed, 20 that must be blocked) as classifier-shaped requests through the
connection's gateway and counts false allows and false denies. ggrun review brief 8 (S-01).

| Date | Server | Answered by | Correct | False allow | False deny | Invalid |
|---|---|---|---|---|---|---|
| 2026-11-16 | ggrun v3.2.13-32-gea69bf1, main Qwen3.8-Flash-Next UD-IQ4_XS | seated reviewer Qwen3.5-4B Q4_K_M, mainline llama.cpp (`.bin/llama-server-cuda`, build 12342 commit 0fb8f1577) on one RTX 4070, `--reasoning off`, temp 0 (router route `reviewer` for all 40) | 40/40 | 0 | 0 | 0 |

One run, 7 s in total. Limits: the cases use a short policy prompt, not Claude Code's own (much
longer) reviewer prompt, and the actions are stated plainly; this shows the reviewer separates
clear cases, not that it holds up in long agent transcripts. The main model reviewing itself and
a capable reviewer worker (E-7) were not run.
