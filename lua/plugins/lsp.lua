return {
  {
    "neovim/nvim-lspconfig",
    dependencies = {
      {
        "folke/lazydev.nvim",
        ft = "lua",
        opts = {
          library = {
            { path = "${3rd}/luv/library", words = { "vim%.uv" } },
          },
        },
      },
      "williamboman/mason.nvim",
      "williamboman/mason-lspconfig.nvim",
    },
    config = function()
      local capabilities = require("cmp_nvim_lsp").default_capabilities()

      -- Keymaps applied whenever an LSP attaches to a buffer
      vim.api.nvim_create_autocmd("LspAttach", {
        group = vim.api.nvim_create_augroup("lsp-keymaps", { clear = true }),
        callback = function(event)
          local map = function(keys, func, desc)
            vim.keymap.set("n", keys, func, { buffer = event.buf, desc = "LSP: " .. desc })
          end
          map("gd",         vim.lsp.buf.definition,     "Go to Definition")
          map("gD",         vim.lsp.buf.declaration,    "Go to Declaration")
          map("gr",         vim.lsp.buf.references,     "Go to References")
          map("gi",         vim.lsp.buf.implementation, "Go to Implementation")
          map("K",          vim.lsp.buf.hover,          "Hover Docs")
          map("<leader>rn", vim.lsp.buf.rename,         "Rename Symbol")
          map("<leader>ca", vim.lsp.buf.code_action,    "Code Action")
          map("<leader>f",  function()
            vim.lsp.buf.format({ async = true })
          end, "Format Buffer")
        end,
      })

      -- Lua LSP
      vim.lsp.enable("lua_ls")

      -- Python: basedpyright — completions, types, go-to-definition
      vim.lsp.config("basedpyright", {
        capabilities = capabilities,
        settings = {
          basedpyright = {
            analysis = {
              autoSearchPaths = true,
              useLibraryCodeForTypes = true,
              diagnosticMode = "openFilesOnly",
            },
          },
        },
      })
      vim.lsp.enable("basedpyright")

      -- Python: ruff — linting and formatting only
      -- Hover disabled so basedpyright's docs are shown instead
      vim.lsp.config("ruff", {
        capabilities = capabilities,
        on_attach = function(client)
          client.server_capabilities.hoverProvider = false
        end,
      })
      vim.lsp.enable("ruff")

      -- Go: gopls — completions, types, go-to-definition, formatting
      vim.lsp.config("gopls", {
        capabilities = capabilities,
        settings = {
          gopls = {
            analyses = {
              unusedparams = true,
              shadow = true,
            },
            staticcheck = true,
            gofumpt = true,
          },
        },
      })
      vim.lsp.enable("gopls")
    end,
  },
}
