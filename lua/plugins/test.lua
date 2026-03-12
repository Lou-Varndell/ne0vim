return {
  "nvim-neotest/neotest",
  dependencies = {
    "nvim-neotest/nvim-nio",
    "nvim-lua/plenary.nvim",
    "antoinemadec/FixCursorHold.nvim",
    "nvim-treesitter/nvim-treesitter",
    "nvim-neotest/neotest-python",
  },
  keys = {
    { "<leader>tn", function() require("neotest").run.run() end,                          desc = "Test: Run Nearest" },
    { "<leader>tf", function() require("neotest").run.run(vim.fn.expand("%")) end,         desc = "Test: Run File" },
    { "<leader>tl", function() require("neotest").run.run_last() end,                      desc = "Test: Run Last" },
    { "<leader>ts", function() require("neotest").summary.toggle() end,                    desc = "Test: Toggle Summary" },
    { "<leader>to", function() require("neotest").output.open({ enter = true }) end,       desc = "Test: Open Output" },
    { "<leader>tO", function() require("neotest").output_panel.toggle() end,               desc = "Test: Toggle Output Panel" },
    { "<leader>td", function() require("neotest").run.run({ strategy = "dap" }) end,       desc = "Test: Debug Nearest" },
  },
  config = function()
    require("neotest").setup({
      adapters = {
        require("neotest-python")({
          -- Wire the nearest test debugger into nvim-dap
          dap = { justMyCode = false },
          runner = "pytest",
          -- Walk up from cwd to find a UV-managed .venv, then fall back to
          -- whatever python is on PATH (e.g. `uv run python`).
          python = function()
            local venv_dir = vim.fn.finddir(".venv", vim.fn.getcwd() .. ";")
            if venv_dir ~= "" then
              return vim.fn.fnamemodify(venv_dir, ":p") .. "bin/python"
            end
            return vim.fn.exepath("python3") ~= "" and vim.fn.exepath("python3")
              or vim.fn.exepath("python")
              or "python"
          end,
          -- Pass pytest args; add -x to stop on first failure if preferred.
          pytest_discovery_args = { "--no-header", "-rN" },
        }),
      },
    })
  end,
}
