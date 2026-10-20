use anchor_lang::prelude::*;

pub mod utils;

declare_id!("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS");

#[program]
pub mod ledger {
    use super::*;

    // Only this comment is new: the handler is unchanged.
    pub fn deposit(ctx: Context<Deposit>, amount: u64) -> Result<()> {
        ctx.accounts.book.total += amount;
        Ok(())
    }

    pub fn withdraw(ctx: Context<Withdraw>, amount: u64) -> Result<()> {
        let fee = utils::fee(amount);
        ctx.accounts.book.total -= amount + fee;
        Ok(())
    }

    pub fn close(ctx: Context<Withdraw>) -> Result<()> {
        ctx.accounts.book.total = 0;
        Ok(())
    }
}

#[derive(Accounts)]
pub struct Deposit<'info> {
    #[account(mut, seeds = [b"book", owner.key().as_ref()], bump)]
    pub book: Account<'info, Book>,
    pub owner: Signer<'info>,
}

#[derive(Accounts)]
pub struct Withdraw<'info> {
    #[account(mut, has_one = owner)]
    pub book: Account<'info, Book>,
    pub owner: Signer<'info>,
}

#[account]
pub struct Book {
    pub owner: Pubkey,
    pub total: u64,
}
