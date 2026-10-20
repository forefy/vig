use solana_program::{account_info::{next_account_info, AccountInfo}, entrypoint, entrypoint::ProgramResult, program::invoke_signed, pubkey::Pubkey, system_instruction};

pub mod instruction;
pub mod processor;

entrypoint!(process_instruction);

pub fn process_instruction(program_id: &Pubkey, accounts: &[AccountInfo], data: &[u8]) -> ProgramResult {
    processor::Processor::process(program_id, accounts, data)
}
