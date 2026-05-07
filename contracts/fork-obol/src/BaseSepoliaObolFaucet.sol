// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IBaseSepoliaObolToken {
    function balanceOf(address account) external view returns (uint256);
    function transfer(address to, uint256 value) external returns (bool);
}

/// @notice Bounded Base Sepolia OBOL faucet for x402 smoke tests.
/// @dev Seed this contract with BaseSepoliaObolToken. Claims transfer from the
///      faucet balance and are rate-limited per caller, so the faucet cannot
///      emit more tokens than it has been explicitly funded with.
contract BaseSepoliaObolFaucet {
    address public immutable token;
    address public owner;
    uint256 public claimAmount;
    uint256 public cooldown;

    mapping(address => uint256) public nextClaimAt;

    event Claimed(address indexed caller, address indexed to, uint256 amount, uint256 nextClaimAt);
    event ClaimTermsUpdated(uint256 claimAmount, uint256 cooldown);
    event FaucetWithdrawal(address indexed to, uint256 amount);
    event OwnershipTransferred(address indexed previousOwner, address indexed newOwner);

    modifier onlyOwner() {
        require(msg.sender == owner, "Faucet: caller is not owner");
        _;
    }

    constructor(address token_, address owner_, uint256 claimAmount_, uint256 cooldown_) {
        require(token_ != address(0), "Faucet: token is zero address");
        require(owner_ != address(0), "Faucet: owner is zero address");
        require(claimAmount_ > 0, "Faucet: claim amount is zero");

        token = token_;
        owner = owner_;
        claimAmount = claimAmount_;
        cooldown = cooldown_;

        emit OwnershipTransferred(address(0), owner_);
        emit ClaimTermsUpdated(claimAmount_, cooldown_);
    }

    function claim() external {
        _claim(msg.sender, msg.sender);
    }

    function claim(address to) external {
        _claim(msg.sender, to);
    }

    function setClaimTerms(uint256 claimAmount_, uint256 cooldown_) external onlyOwner {
        require(claimAmount_ > 0, "Faucet: claim amount is zero");
        claimAmount = claimAmount_;
        cooldown = cooldown_;
        emit ClaimTermsUpdated(claimAmount_, cooldown_);
    }

    function withdraw(address to, uint256 amount) external onlyOwner {
        require(to != address(0), "Faucet: withdraw to zero address");
        _transferToken(to, amount);
        emit FaucetWithdrawal(to, amount);
    }

    function transferOwnership(address newOwner) external onlyOwner {
        require(newOwner != address(0), "Faucet: new owner is zero address");
        emit OwnershipTransferred(owner, newOwner);
        owner = newOwner;
    }

    function _claim(address caller, address to) internal {
        require(to != address(0), "Faucet: claim to zero address");
        require(block.timestamp >= nextClaimAt[caller], "Faucet: cooldown active");
        require(
            IBaseSepoliaObolToken(token).balanceOf(address(this)) >= claimAmount,
            "Faucet: insufficient balance"
        );

        uint256 nextClaim = block.timestamp + cooldown;
        nextClaimAt[caller] = nextClaim;
        _transferToken(to, claimAmount);
        emit Claimed(caller, to, claimAmount, nextClaim);
    }

    function _transferToken(address to, uint256 amount) internal {
        require(IBaseSepoliaObolToken(token).transfer(to, amount), "Faucet: transfer failed");
    }
}
