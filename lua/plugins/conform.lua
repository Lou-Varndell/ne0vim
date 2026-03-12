return {
  "stevearc/conform.nvim",
  event = { "BufWritePre" },
  cmd = { "ConformInfo" },
  keys = {
    {
      "<leader>F",
      function()
        require("conform").format({ async = true, lsp_fallback = true })
      end,
      desc = "Format: Conform (explicit)",
    },
  },
  config = function()
    require("conform").setup({
      formatters_by_ft = {
        python = {
          -- ruff_organize_imports runs first (sorts/removes imports),
          -- then ruff_format applies Black-compatible style.
          "ruff_organize_imports",
          "ruff_format",
        },
      },
      format_on_save = {
        timeout_ms = 500,
        lsp_fallback = true,
      },
      -- conform will use the ruff binary found in the active UV venv ($PATH),
      -- falling back to the Mason-installed ruff if none is active.
    })
  end,
}
