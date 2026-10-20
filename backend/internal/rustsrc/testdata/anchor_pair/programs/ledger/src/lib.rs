use anchor_lang::prelude::*;

declare_id!("HmbTLCmaGvZhKnn1Zfa1JVnp7vkMV4DYVxPLWBVoN65L");

#[program]
pub mod book_ledger {
    use super::*;
    pub fn record(ctx: Context<Record>, amount: u64) -> Result<()> {
        ctx.accounts.page.total += amount;
        Ok(())
    }
}

#[derive(Accounts)]
pub struct Record<'info> {
    pub desk: Signer<'info>,
    #[account(mut)]
    pub page: Account<'info, Page>,
}

#[account]
pub struct Page {
    pub total: u64,
}
