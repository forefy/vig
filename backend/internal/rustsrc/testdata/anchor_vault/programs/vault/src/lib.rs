use anchor_lang::prelude::*;
use anchor_lang::system_program::Transfer as SysTransfer;
use anchor_spl::token::{self, Mint, MintTo, Token, TokenAccount, Transfer};

pub mod state;
pub mod utils;
use state::*;

declare_id!("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS");

/* A block comment with a { brace and a TODO inside it. */
#[program]
pub mod vault {
    use super::*;

    /// Creates the vault. TODO: charge rent from the treasury.
    pub fn initialize(ctx: Context<Initialize>, amount: u64, cfg: Config) -> Result<()> {
        let v = &mut ctx.accounts.vault;
        v.owner = ctx.accounts.owner.key();
        v.amount = amount;
        v.cfg = cfg;
        msg!("init {}", amount); // braces in a string: {}
        utils::deposit(ctx.accounts.into_transfer(), amount)?;
        utils::reward(ctx.accounts.system_program.to_account_info(), ctx.accounts.token_program.to_account_info(), MintTo { mint: ctx.accounts.tokens.mint.to_account_info(), to: ctx.accounts.tokens.escrow.to_account_info(), authority: ctx.accounts.owner.to_account_info() }, SysTransfer { from: ctx.accounts.owner.to_account_info(), to: ctx.accounts.vault.to_account_info() })?;
        Ok(())
    }

    pub fn withdraw(ctx: Context<Withdraw>, amount: u64) -> Result<()> {
        let (_pda, bump) = Pubkey::find_program_address(&[b"vault", ctx.accounts.owner.key().as_ref()], ctx.program_id);
        let seeds: &[&[u8]] = &[b"vault", ctx.accounts.owner.key.as_ref(), &[bump]];
        let cpi = CpiContext::new_with_signer(
            ctx.accounts.token_program.to_account_info(),
            Transfer { from: ctx.accounts.escrow.to_account_info(), to: ctx.accounts.to.to_account_info(), authority: ctx.accounts.vault.to_account_info() },
            &[seeds],
        );
        token::transfer(cpi, amount)?;
        let remaining = ctx.remaining_accounts.len();
        let fee = shared::checked_fee(amount, 30);
        ctx.accounts.process()?;
        **ctx.accounts.vault.to_account_info().try_borrow_mut_lamports()? -= fee;
        Ok(())
    }

    pub fn close(ctx: Context<Close>) -> Result<()> {
        Vault::finish(&ctx.accounts.vault)
    }

    fn not_a_handler(x: u64) -> u64 {
        x + 1
    }
}

#[derive(Accounts)]
#[instruction(amount: u64)]
pub struct Initialize<'info> {
    #[account(
        init,
        payer = owner,
        space = 8 + Vault::LEN,
        seeds = [
            Vault::SEED,
            owner.key().as_ref(),
            token::ID.as_ref()
        ],
        bump
    )]
    pub vault: Account<'info, Vault>,
    #[account(mut)]
    pub owner: Signer<'info>,
    pub tokens: TokenPair<'info>,
    pub system_program: Program<'info, System>,
    pub token_program: Program<'info, Token>,
}

#[derive(Accounts)]
pub struct TokenPair<'info> {
    pub mint: Account<'info, Mint>,
    #[account(mut, constraint = escrow.mint == mint.key())]
    pub escrow: Box<Account<'info, TokenAccount>>,
}

#[derive(Accounts)]
pub struct Withdraw<'info> {
    #[account(mut, has_one = owner @ VaultError::Owner, seeds = [b"vault", owner.key().as_ref()], bump, realloc = 8 + 64, realloc::payer = owner, realloc::zero = false)]
    pub vault: Account<'info, Vault>,
    pub owner: Signer<'info>,
    #[account(mut)]
    pub escrow: Account<'info, TokenAccount>,
    #[account(mut)]
    pub to: Account<'info, TokenAccount>,
    /// CHECK: any account, validated in the handler
    pub anyone: UncheckedAccount<'info>,
    pub maybe: Option<AccountInfo<'info>>,
    #[account(address = anchor_spl::token::ID)]
    pub token_program: Program<'info, Token>,
}

#[derive(Accounts)]
pub struct Close<'info> {
    #[account(mut, close = owner, owner = crate::ID)]
    pub vault: Account<'info, Vault>,
    #[account(mut)]
    pub owner: Signer<'info>,
    pub big: AccountLoader<'info, Big>,
}

impl<'info> Initialize<'info> {
    pub fn into_transfer(&self) -> CpiContext<'_, '_, '_, 'info, Transfer<'info>> {
        CpiContext::new(
            self.token_program.to_account_info(),
            Transfer { from: self.tokens.escrow.to_account_info(), to: self.tokens.escrow.to_account_info(), authority: self.owner.to_account_info() },
        )
    }
}

#[error_code]
pub enum VaultError {
    #[msg("wrong owner")]
    Owner,
}

impl<'info> Withdraw<'info> {
    pub fn process(&mut self) -> Result<()> {
        self.check()
    }
    fn check(&self) -> Result<()> {
        Pubkey::find_program_address(&[b"check"], &crate::ID);
        Ok(())
    }
}

impl<'info> Close<'info> {
    pub fn process(&mut self) -> Result<()> {
        Ok(())
    }
}
