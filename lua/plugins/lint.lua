return {
  "mfussenegger/nvim-lint",
  -- ruff LSP already shows inline diagnostics; nvim-lint adds an explicit
  -- triggered pass (useful for catching issues LSP misses at file-open time
  -- and for CI-style severity enforcement via ruff's exit code).
  event = { "BufReadPost", "BufWritePost" },
  config = function()
    local lint = require("lint")

    lint.linters_by_ft = {
      python = { "ruff" },
    }

    -- Avoid double-firing; InsertLeave keeps diagnostics fresh without
    -- triggering on every keystroke.
    vim.api.nvim_create_autocmd({ "BufReadPost", "BufWritePost", "InsertLeave" }, {
      group = vim.api.nvim_create_augroup("nvim-lint", { clear = true }),
      callback = function()
        lint.try_lint()
      end,
    })
  end,
}
