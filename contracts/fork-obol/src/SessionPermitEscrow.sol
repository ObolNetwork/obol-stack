// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IERC20PermitLike {
    function permit(
        address owner,
        address spender,
        uint256 value,
        uint256 deadline,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external;

    function transferFrom(address from, address to, uint256 value) external returns (bool);
    function transfer(address to, uint256 value) external returns (bool);
}

contract SessionPermitEscrow {
    struct Session {
        address owner;
        address seller;
        address token;
        uint256 amount;
        bool closed;
    }

    address public immutable facilitator;
    mapping(bytes32 => Session) public sessions;

    event SessionAuthorized(
        bytes32 indexed sessionId,
        address indexed owner,
        address indexed seller,
        address token,
        uint256 amount
    );
    event SessionClosed(bytes32 indexed sessionId, uint256 spent, uint256 refunded);

    error SessionAlreadyExists();
    error SessionNotFound();
    error SessionClosedAlready();
    error SpendExceedsAuthorized();
    error Unauthorized();
    error InvalidSignatureLength();

    constructor(address facilitator_) {
        facilitator = facilitator_;
    }

    function sessionIdFor(
        address owner,
        address seller,
        address token,
        uint256 amount,
        bytes32 salt
    ) public pure returns (bytes32) {
        return keccak256(abi.encode(owner, seller, token, amount, salt));
    }

    function authorizeWithPermitAndDeposit(
        address token,
        address owner,
        address seller,
        uint256 amount,
        uint256 deadline,
        bytes calldata signature,
        bytes32 salt
    ) external returns (bytes32 sessionId) {
        sessionId = sessionIdFor(owner, seller, token, amount, salt);
        if (sessions[sessionId].owner != address(0)) revert SessionAlreadyExists();

        (uint8 v, bytes32 r, bytes32 s) = _splitSignature(signature);
        IERC20PermitLike(token).permit(owner, address(this), amount, deadline, v, r, s);
        require(IERC20PermitLike(token).transferFrom(owner, address(this), amount), "transferFrom failed");

        sessions[sessionId] = Session({
            owner: owner,
            seller: seller,
            token: token,
            amount: amount,
            closed: false
        });

        emit SessionAuthorized(sessionId, owner, seller, token, amount);
    }

    function close(bytes32 sessionId, uint256 spent) external {
        if (msg.sender != facilitator) revert Unauthorized();

        Session storage session = sessions[sessionId];
        if (session.owner == address(0)) revert SessionNotFound();
        if (session.closed) revert SessionClosedAlready();
        if (spent > session.amount) revert SpendExceedsAuthorized();

        session.closed = true;

        uint256 refund = session.amount - spent;
        if (spent > 0) {
            require(IERC20PermitLike(session.token).transfer(session.seller, spent), "seller transfer failed");
        }
        if (refund > 0) {
            require(IERC20PermitLike(session.token).transfer(session.owner, refund), "refund transfer failed");
        }

        emit SessionClosed(sessionId, spent, refund);
    }

    function _splitSignature(
        bytes calldata signature
    ) internal pure returns (uint8 v, bytes32 r, bytes32 s) {
        if (signature.length != 65) revert InvalidSignatureLength();
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
    }
}
