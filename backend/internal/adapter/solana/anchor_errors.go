package solana

// anchorErrors names the framework's own error codes, which no IDL lists:
// constraint failures in the 2000s, account checks in the 3000s.
var anchorErrors = map[int]string{
	2000: "ConstraintMut", 2001: "ConstraintHasOne", 2002: "ConstraintSigner", 2003: "ConstraintRaw", 2004: "ConstraintOwner",
	2005: "ConstraintRentExempt", 2006: "ConstraintSeeds", 2007: "ConstraintExecutable", 2008: "ConstraintState", 2009: "ConstraintAssociated",
	2010: "ConstraintAssociatedInit", 2011: "ConstraintClose", 2012: "ConstraintAddress", 2013: "ConstraintZero", 2014: "ConstraintTokenMint",
	2015: "ConstraintTokenOwner", 2016: "ConstraintMintMintAuthority", 2017: "ConstraintMintFreezeAuthority", 2018: "ConstraintMintDecimals", 2019: "ConstraintSpace",
	3000: "AccountDiscriminatorAlreadySet", 3001: "AccountDiscriminatorNotFound", 3002: "AccountDiscriminatorMismatch", 3003: "AccountDidNotDeserialize",
	3004: "AccountDidNotSerialize", 3005: "AccountNotEnoughKeys", 3006: "AccountNotMutable", 3007: "AccountOwnedByWrongProgram", 3008: "InvalidProgramId",
	3009: "InvalidProgramExecutable", 3010: "AccountNotSigner", 3011: "AccountNotSystemOwned", 3012: "AccountNotInitialized", 3013: "AccountNotProgramData",
	3014: "AccountNotAssociatedTokenAccount", 3015: "AccountSysvarMismatch", 3016: "AccountReallocExceedsLimit", 3017: "AccountDuplicateReallocs",
	100: "InstructionMissing", 101: "InstructionFallbackNotFound", 102: "InstructionDidNotDeserialize", 103: "InstructionDidNotSerialize",
	1000: "IdlInstructionStub", 1001: "IdlInstructionInvalidProgram", 4100: "Deprecated",
}
