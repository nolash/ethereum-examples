pragma solidity ^0.4.0;

contract Store {
	bool public foo;
	function poke() {
		foo = true;
	}
}
