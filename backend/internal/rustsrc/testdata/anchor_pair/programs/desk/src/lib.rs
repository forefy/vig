use anchor_lang::prelude::*;
use book_ledger::program::BookLedger;

declare_id!("Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS");

#[program]
pub mod desk {
    use super::*;
    pub fn post(ctx: Context<Post>, amount: u64) -> Result<()> {
        let seeds: &[&[u8]] = &[b"desk", &[ctx.bumps.desk]];
        let cpi = CpiContext::new_with_signer(
            ctx.accounts.ledger_program.to_account_info(),
            book_ledger::cpi::accounts::Record { desk: ctx.accounts.desk.to_account_info(), page: ctx.accounts.page.to_account_info() },
            &[seeds],
        );
        book_ledger::cpi::record(cpi, amount)
    }
}

#[derive(Accounts)]
pub struct Post<'info> {
    #[account(seeds = [b"desk"], bump)]
    pub desk: SystemAccount<'info>,
    /// CHECK: owned by the ledger
    #[account(mut)]
    pub page: UncheckedAccount<'info>,
    pub ledger_program: Program<'info, BookLedger>,
}
