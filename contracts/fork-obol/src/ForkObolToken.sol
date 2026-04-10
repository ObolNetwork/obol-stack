// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

contract ForkObolToken {
    string public constant name = "Obol Network";
    string public constant symbol = "OBOL";
    uint8 public constant decimals = 18;

    uint256 public totalSupply;

    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    mapping(address => uint256) public nonces;

    bytes32 private constant _PERMIT_TYPEHASH =
        keccak256("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)");
    bytes32 private constant _EIP712_DOMAIN_TYPEHASH =
        keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");
    bytes32 private constant _NAME_HASH = keccak256(bytes("Obol Network"));
    bytes32 private constant _VERSION_HASH = keccak256(bytes("1"));

    uint256 private immutable _initialChainId;
    bytes32 private immutable _initialDomainSeparator;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);

    constructor(address initialHolder, uint256 initialSupply) {
        _initialChainId = block.chainid;
        _initialDomainSeparator = _computeDomainSeparator();
        _mint(initialHolder, initialSupply);
    }

    function DOMAIN_SEPARATOR() external view returns (bytes32) {
        return _domainSeparator();
    }

    function transfer(address to, uint256 value) external returns (bool) {
        _transfer(msg.sender, to, value);
        return true;
    }

    function approve(address spender, uint256 value) external returns (bool) {
        allowance[msg.sender][spender] = value;
        emit Approval(msg.sender, spender, value);
        return true;
    }

    function transferFrom(address from, address to, uint256 value) external returns (bool) {
        uint256 allowed = allowance[from][msg.sender];
        require(allowed >= value, "ERC20: insufficient allowance");
        if (allowed != type(uint256).max) {
            unchecked {
                allowance[from][msg.sender] = allowed - value;
            }
            emit Approval(from, msg.sender, allowance[from][msg.sender]);
        }
        _transfer(from, to, value);
        return true;
    }

    function mint(address to, uint256 value) external {
        _mint(to, value);
    }

    function permit(
        address owner,
        address spender,
        uint256 value,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external {
        require(block.timestamp <= deadline, "ERC20Permit: expired deadline");

        uint256 nonce = nonces[owner]++;
        bytes32 structHash = keccak256(
            abi.encode(_PERMIT_TYPEHASH, owner, spender, value, nonce, deadline)
        );
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", _domainSeparator(), structHash));
        address recovered = ecrecover(digest, v, r, s);
        require(recovered != address(0) && recovered == owner, "ERC20Permit: invalid signature");

        allowance[owner][spender] = value;
        emit Approval(owner, spender, value);
    }

    function _transfer(address from, address to, uint256 value) internal {
        require(to != address(0), "ERC20: transfer to the zero address");
        uint256 fromBalance = balanceOf[from];
        require(fromBalance >= value, "ERC20: transfer amount exceeds balance");
        unchecked {
            balanceOf[from] = fromBalance - value;
            balanceOf[to] += value;
        }
        emit Transfer(from, to, value);
    }

    function _mint(address to, uint256 value) internal {
        require(to != address(0), "ERC20: mint to the zero address");
        totalSupply += value;
        balanceOf[to] += value;
        emit Transfer(address(0), to, value);
    }

    function _domainSeparator() internal view returns (bytes32) {
        if (block.chainid == _initialChainId) {
            return _initialDomainSeparator;
        }
        return _computeDomainSeparator();
    }

    function _computeDomainSeparator() internal view returns (bytes32) {
        return keccak256(
            abi.encode(
                _EIP712_DOMAIN_TYPEHASH,
                _NAME_HASH,
                _VERSION_HASH,
                block.chainid,
                address(this)
            )
        );
    }
}
