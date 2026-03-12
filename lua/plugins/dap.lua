return {
  {
    "mfussenegger/nvim-dap",
    dependencies = {
      "mfussenegger/nvim-dap-python",
      "leoluz/nvim-dap-go",
      {
        "rcarriga/nvim-dap-ui",
        dependencies = { "nvim-neotest/nvim-nio" },
      },
    },
    config = function()
      local dap = require("dap")
      local dapui = require("dapui")
      local dap_python = require("dap-python")
      local dap_go = require("dap-go")

      -- Auto open/close the UI on debug session events
      dap.listeners.after.event_initialized["dapui_config"] = function()
        dapui.open()
      end
      dap.listeners.before.event_terminated["dapui_config"] = function()
        dapui.close()
      end
      dap.listeners.before.event_exited["dapui_config"] = function()
        dapui.close()
      end

      dapui.setup()

      -- Use Mason's managed debugpy. Mason installs it at a fixed path so it
      -- works regardless of whether a UV venv is active or not.
      local debugpy_python = vim.fn.stdpath("data") .. "/mason/packages/debugpy/venv/bin/python"
      dap_python.setup(debugpy_python)
      dap_go.setup()

      -- Keymaps
      vim.keymap.set("n", "<F5>",        dap.continue,             { desc = "DAP: Continue" })
      vim.keymap.set("n", "<F10>",       dap.step_over,            { desc = "DAP: Step Over" })
      vim.keymap.set("n", "<F11>",       dap.step_into,            { desc = "DAP: Step Into" })
      vim.keymap.set("n", "<F12>",       dap.step_out,             { desc = "DAP: Step Out" })
      vim.keymap.set("n", "<leader>db",  dap.toggle_breakpoint,    { desc = "DAP: Toggle Breakpoint" })
      vim.keymap.set("n", "<leader>du",  dapui.toggle,             { desc = "DAP: Toggle UI" })
      vim.keymap.set("n", "<leader>dt",  dap_go.debug_test,        { desc = "DAP: Debug Go Test" })
    end,
  },
}
