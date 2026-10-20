use anchor_lang::prelude::*;
use anchor_lang::system_program::{transfer as sys_transfer, Transfer as SysTransfer};
use anchor_spl::token::{self, mint_to, MintTo, Transfer};

pub fn deposit<'info>(cpi: CpiContext<'_, '_, '_, 'info, Transfer<'info>>, amount: u64) -> Result<()> {
    token::transfer(cpi, amount)
}

pub fn reward<'info>(system_program: AccountInfo<'info>, token_program: AccountInfo<'info>, mint: MintTo<'info>, sys: SysTransfer<'info>) -> Result<()> {
    mint_to(CpiContext::new(token_program, mint), 1)?;
    transfer(CpiContext::new(system_program, sys), 1)
}

fn transfer<'info>(cpi: CpiContext<'_, '_, '_, 'info, SysTransfer<'info>>, lamports: u64) -> Result<()> {
    sys_transfer(cpi, lamports)
}

// FIXME: unused
pub fn finish() {}
